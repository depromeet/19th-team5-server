package room

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
)

type fakeConn struct {
	mu     sync.Mutex
	sent   []string
	closed bool
	code   int
	reason string
}

func (c *fakeConn) send(msg []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, string(msg))
}

func (c *fakeConn) close(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.code = code
	c.reason = reason
}

func (c *fakeConn) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sent))
	copy(out, c.sent)
	return out
}

func (c *fakeConn) closeState() (bool, int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed, c.code, c.reason
}

type fakeStore struct {
	mu      sync.Mutex
	members []Member
	err     error
	calls   int
	block   chan struct{}
}

func (s *fakeStore) Participants(ctx context.Context, _ string) ([]Member, error) {
	s.mu.Lock()
	s.calls++
	block := s.block
	members := s.members
	err := s.err
	s.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return members, err
}

func defaultMembers() []Member {
	return []Member{
		{MemberID: "m1", Nickname: "Alice", Role: RoleInterviewer},
		{MemberID: "m2", Nickname: "Bob", Role: RoleInterviewee},
	}
}

func mustMarshal(t *testing.T, typ string, payload any) string {
	t.Helper()
	data, err := marshalServerMessage(typ, payload)
	if err != nil {
		t.Fatalf("marshalServerMessage(%q) failed: %v", typ, err)
	}
	return string(data)
}

func wantError(t *testing.T, cause, field string) string {
	t.Helper()
	return mustMarshal(t, typeError, (&parseError{cause: cause, field: field}).payload())
}

func TestParseContractsAndErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		cause string
		field string
	}{
		{
			name:  "invalid_json",
			input: `{`,
			cause: causeInvalidJSON,
		},
		{
			name:  "invalid_utf8",
			input: "{\"type\":\"room.join\",\"payload\":{\"interviewId\":\"\xff\"}}",
			cause: causeInvalidJSON,
		},
		{
			name:  "duplicate_field",
			input: `{"type":"room.join","type":"room.leave"}`,
			cause: causeDuplicateField,
		},
		{
			name:  "payload_not_object",
			input: `{"type":"room.join","payload":[]}`,
			cause: causePayloadNotObject,
			field: "/payload",
		},
		{
			name:  "unknown_type",
			input: `{"type":"room.unknown"}`,
			cause: causeUnknownType,
			field: "/type",
		},
		{
			name:  "wrong_field_type",
			input: `{"type":"room.join","payload":{"interviewId":123}}`,
			cause: causeWrongFieldType,
			field: "/payload/interviewId",
		},
		{
			name:  "case_sensitive_field",
			input: `{"type":"room.join","payload":{"InterviewId":"i1"}}`,
			cause: causeMissingField,
			field: "/payload/interviewId",
		},
		{
			name:  "null_payload_on_join",
			input: `{"type":"room.join","payload":null}`,
			cause: causeMissingField,
			field: "/payload/interviewId",
		},
		{
			name:  "leave_before_join",
			input: `{"type":"room.leave"}`,
			cause: causeNotJoined,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHub(&fakeStore{members: defaultMembers()}, slog.New(slog.DiscardHandler))
			c := &fakeConn{}
			s := h.newSession("m1", c)
			s.handleMessage(t.Context(), []byte(tc.input))

			want := []string{wantError(t, tc.cause, tc.field)}
			if diff := cmp.Diff(want, c.messages()); diff != "" {
				t.Errorf("handleMessage(%s) diff (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestRoomJoinIgnoresUnknownFieldsAndRejectsAlreadyJoined(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		h := newHub(&fakeStore{members: defaultMembers()}, slog.New(slog.DiscardHandler))
		c := &fakeConn{}
		s := h.newSession("m1", c)

		s.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1","extra":true}}`))
		synctest.Wait()

		s.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		want := []string{
			mustMarshal(t, typeJoined, joinedPayload{ParticipantID: "p1"}),
			mustMarshal(t, typeParticipants, participantsPayload{
				Participants: []participantPayload{
					{ParticipantID: "p1", Nickname: "Alice", Role: RoleInterviewer, State: StateConnected},
				},
			}),
			wantError(t, causeAlreadyJoined, ""),
		}
		if diff := cmp.Diff(want, c.messages()); diff != "" {
			t.Errorf("handleMessage(room.join, room.join) sent messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomJoinCloseCodes(t *testing.T) {
	tests := []struct {
		name       string
		storeErr   error
		memberID   string
		wantCode   int
		wantReason string
	}{
		{
			name:       "forbidden_member",
			memberID:   "outsider",
			wantCode:   closeForbidden,
			wantReason: "FORBIDDEN",
		},
		{
			name:       "room_not_open",
			storeErr:   ErrRoomNotOpen,
			memberID:   "m1",
			wantCode:   closeRoomNotOpen,
			wantReason: "ROOM_NOT_OPEN",
		},
		{
			name:       "store_failure",
			storeErr:   errors.New("core unavailable"),
			memberID:   "m1",
			wantCode:   closeTryAgainLater,
			wantReason: "Try Again Later",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				store := &fakeStore{members: defaultMembers(), err: tc.storeErr}
				h := newHub(store, slog.New(slog.DiscardHandler))
				c := &fakeConn{}
				s := h.newSession(tc.memberID, c)

				s.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
				synctest.Wait()

				closed, code, reason := c.closeState()
				if !closed || code != tc.wantCode || reason != tc.wantReason {
					t.Errorf("handleMessage(room.join) close = (%t, %d, %q), want (true, %d, %q)", closed, code, reason, tc.wantCode, tc.wantReason)
				}
				h.mu.Lock()
				remaining := len(h.rooms)
				h.mu.Unlock()
				if remaining != 0 {
					t.Errorf("remaining rooms = %d, want 0", remaining)
				}
			})
		})
	}
}

func TestRoomDisconnectReconnectAndGraceExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		store := &fakeStore{members: defaultMembers()}
		h := newHub(store, slog.New(slog.DiscardHandler))

		c1, c2 := &fakeConn{}, &fakeConn{}
		s1, s2 := h.newSession("m1", c1), h.newSession("m2", c2)
		s1.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()
		s2.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// m1 disconnects -> RECONNECTING.
		s1.disconnect(ctx)
		synctest.Wait()

		// m1 reconnects within grace period -> keeps p1 and CONNECTED.
		c1New := &fakeConn{}
		s1New := h.newSession("m1", c1New)
		s1New.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// Advance past grace period and also post a stale grace-expiry event
		// (simulating a timer callback queued right before reconnect stopped it).
		time.Sleep(defaultGracePeriod + time.Second)
		h.mu.Lock()
		r := h.rooms["i1"]
		h.mu.Unlock()
		r.post(func() { r.handleGraceExpired(ctx, "m1", 1) })
		synctest.Wait()

		// m1 disconnects again and lets grace period expire -> removed from list.
		s1New.disconnect(ctx)
		synctest.Wait()
		time.Sleep(defaultGracePeriod + time.Second)
		synctest.Wait()

		// Verify m1 got p1 again on reconnect.
		var joined joinedPayload
		var env struct {
			Type    string         `json:"type"`
			Payload jsontext.Value `json:"payload"`
		}
		msgs := c1New.messages()
		if len(msgs) < 1 {
			t.Fatalf("c1New got no messages")
		}
		if err := json.Unmarshal([]byte(msgs[0]), &env); err != nil {
			t.Fatalf("unmarshal joined: %v", err)
		}
		if err := json.Unmarshal(env.Payload, &joined); err != nil {
			t.Fatalf("unmarshal joined payload: %v", err)
		}
		if joined.ParticipantID != "p1" {
			t.Errorf("reconnected participantId = %q, want p1", joined.ParticipantID)
		}

		// Verify c2 saw: [p1,p2 CONN] -> [p1 RECONN, p2 CONN] -> [p1 CONN, p2 CONN] -> [p1 RECONN, p2 CONN] -> [p2 CONN].
		c2Msgs := c2.messages()
		last := c2Msgs[len(c2Msgs)-1]
		wantLast := mustMarshal(t, typeParticipants, participantsPayload{
			Participants: []participantPayload{
				{ParticipantID: "p2", Nickname: "Bob", Role: RoleInterviewee, State: StateConnected},
			},
		})
		if diff := cmp.Diff(wantLast, last); diff != "" {
			t.Errorf("last participants message to c2 diff (-want +got):\n%s", diff)
		}

		// m2 leaves -> room is removed, next join re-fetches participants.
		s2.handleMessage(ctx, []byte(`{"type":"room.leave"}`))
		synctest.Wait()
		closed, code, _ := c2.closeState()
		if !closed || code != closeNormal {
			t.Errorf("c2 closeState() = (%t, %d), want (true, %d)", closed, code, closeNormal)
		}
		h.mu.Lock()
		remaining := len(h.rooms)
		h.mu.Unlock()
		if remaining != 0 {
			t.Errorf("remaining rooms after all left = %d, want 0", remaining)
		}
		if store.calls != 1 {
			t.Errorf("Participants() calls = %d, want 1", store.calls)
		}
	})
}

func TestRoomReplaceExistingConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		h := newHub(&fakeStore{members: defaultMembers()}, slog.New(slog.DiscardHandler))

		c1 := &fakeConn{}
		s1 := h.newSession("m1", c1)
		s1.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		c2 := &fakeConn{}
		s2 := h.newSession("m1", c2)
		s2.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		closed, code, reason := c1.closeState()
		if !closed || code != closeReplaced || reason != "REPLACED" {
			t.Errorf("c1 closeState() = (%t, %d, %q), want (true, %d, REPLACED)", closed, code, reason, closeReplaced)
		}

		// Late disconnect of replaced connection must not affect the new connection.
		s1.disconnect(ctx)
		synctest.Wait()
		time.Sleep(defaultGracePeriod + time.Second)
		synctest.Wait()

		h.mu.Lock()
		remaining := len(h.rooms)
		h.mu.Unlock()
		if remaining != 1 {
			t.Errorf("remaining rooms after replaced conn disconnected = %d, want 1", remaining)
		}
	})
}

func TestRoomLoadingWaitsForConcurrentJoinAndDropsDisconnectedWaiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		store := &fakeStore{members: defaultMembers(), block: block}
		h := newHub(store, slog.New(slog.DiscardHandler))

		// First client joins with a cancellable context and disconnects while loading.
		ctx1, cancel1 := context.WithCancel(t.Context())
		c1 := &fakeConn{}
		s1 := h.newSession("m1", c1)
		s1.handleMessage(ctx1, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// Second client joins while room is still loading.
		c2 := &fakeConn{}
		s2 := h.newSession("m2", c2)
		s2.handleMessage(t.Context(), []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// First client disconnects and cancels its context before core responds.
		cancel1()
		s1.disconnect(ctx1)
		synctest.Wait()

		// Core responds.
		close(block)
		synctest.Wait()

		if len(c1.messages()) != 0 {
			t.Errorf("disconnected waiter c1 got messages %v, want none", c1.messages())
		}
		wantC2 := []string{
			mustMarshal(t, typeJoined, joinedPayload{ParticipantID: "p1"}),
			mustMarshal(t, typeParticipants, participantsPayload{
				Participants: []participantPayload{
					{ParticipantID: "p1", Nickname: "Bob", Role: RoleInterviewee, State: StateConnected},
				},
			}),
		}
		if diff := cmp.Diff(wantC2, c2.messages()); diff != "" {
			t.Errorf("c2 messages diff (-want +got):\n%s", diff)
		}
		if store.calls != 1 {
			t.Errorf("Participants() calls = %d, want 1", store.calls)
		}
	})
}

type fakeVoiceRoom struct {
	mu     sync.Mutex
	send   func(ctx context.Context, participantID string, msg []byte)
	events []string
}

func (v *fakeVoiceRoom) record(e string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.events = append(v.events, e)
}

func (v *fakeVoiceRoom) Receive(_ context.Context, pid string, data []byte) {
	v.record("receive:" + pid + ":" + string(data))
}

func (v *fakeVoiceRoom) Leave(_ context.Context, pid string) {
	v.record("leave:" + pid)
}

func (v *fakeVoiceRoom) Disconnect(_ context.Context, pid string) {
	v.record("disconnect:" + pid)
}

func (v *fakeVoiceRoom) Reconnect(_ context.Context, pid string) {
	v.record("reconnect:" + pid)
}

func (v *fakeVoiceRoom) snapshot() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, len(v.events))
	copy(out, v.events)
	return out
}

func TestRoomVoiceRoutingAndNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		var vr *fakeVoiceRoom
		h := newHub(&fakeStore{members: defaultMembers()}, slog.New(slog.DiscardHandler))
		h.newVoiceRoom = func(send func(ctx context.Context, participantID string, msg []byte)) VoiceRoom {
			vr = &fakeVoiceRoom{send: send}
			return vr
		}

		c1 := &fakeConn{}
		s1 := h.newSession("m1", c1)
		// voice.* before join returns not-joined.
		s1.handleMessage(ctx, []byte(`{"type":"voice.join"}`))
		if diff := cmp.Diff([]string{wantError(t, causeNotJoined, "")}, c1.messages()); diff != "" {
			t.Fatalf("voice.join before room.join diff (-want +got):\n%s", diff)
		}

		s1.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// Forward voice.* to VoiceRoom and deliver server voice.* back to conn.
		s1.handleMessage(ctx, []byte(`{"type":"voice.join"}`))
		synctest.Wait()
		vr.send(ctx, "p1", []byte(`{"type":"voice.offer","payload":{"sdp":"v=0","offerId":1}}`))
		synctest.Wait()

		msgs := c1.messages()
		if got := msgs[len(msgs)-1]; got != `{"type":"voice.offer","payload":{"sdp":"v=0","offerId":1}}` {
			t.Errorf("last message to c1 = %s, want voice.offer", got)
		}

		// Replace c1 with c1New (4009) -> triggers voice Reconnect; old conn events are ignored.
		c1New := &fakeConn{}
		s1New := h.newSession("m1", c1New)
		s1New.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()
		s1.handleMessage(ctx, []byte(`{"type":"voice.join"}`))
		s1.handleMessage(ctx, []byte(`{"type":"room.leave"}`))
		s1.disconnect(ctx)
		synctest.Wait()

		// Voice message to unknown participant ID is ignored.
		vr.send(ctx, "unknown", []byte(`{"type":"voice.offer","payload":{"sdp":"v=0","offerId":9}}`))
		synctest.Wait()

		// Disconnect c1New -> voice Disconnect; message while disconnected is dropped.
		s1New.disconnect(ctx)
		synctest.Wait()
		vr.send(ctx, "p1", []byte(`{"type":"voice.offer","payload":{"sdp":"v=0","offerId":2}}`))
		synctest.Wait()

		// m2 joins and explicitly leaves via room.leave -> voice Leave.
		c2 := &fakeConn{}
		s2 := h.newSession("m2", c2)
		s2.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()
		s2.handleMessage(ctx, []byte(`{"type":"room.leave"}`))
		synctest.Wait()

		// Grace expires -> voice Leave.
		time.Sleep(defaultGracePeriod + time.Second)
		synctest.Wait()

		// Disconnect before joining is a no-op.
		c3 := &fakeConn{}
		h.newSession("m2", c3).disconnect(ctx)

		wantEvents := []string{
			`receive:p1:{"type":"voice.join"}`,
			"reconnect:p1",
			"disconnect:p1",
			"leave:p2",
			"leave:p1",
		}
		if diff := cmp.Diff(wantEvents, vr.snapshot()); diff != "" {
			t.Errorf("voice events diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomQueuedJoinAfterLeaveKeepsRoomAndLoadFailureClosesQueuedJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		h := newHub(&fakeStore{members: defaultMembers()}, slog.New(slog.DiscardHandler))

		c1 := &fakeConn{}
		s1 := h.newSession("m1", c1)
		s1.handleMessage(ctx, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`))
		synctest.Wait()

		// Queue handleLeave followed immediately by handleJoin on the same room
		// while blocking the room goroutine so both sit in r.queue together.
		h.mu.Lock()
		r := h.rooms["i1"]
		h.mu.Unlock()

		release := make(chan struct{})
		r.post(func() { <-release })
		synctest.Wait()

		c2 := &fakeConn{}
		s2 := h.newSession("m2", c2)
		r.post(func() { r.handleLeave(ctx, "m1", c1) })
		r.post(func() { r.handleJoin(ctx, s2) })
		close(release)
		synctest.Wait()

		h.mu.Lock()
		gotRoom := h.rooms["i1"]
		h.mu.Unlock()
		if gotRoom != r {
			t.Errorf("room i1 was removed despite queued join; got %p, want %p", gotRoom, r)
		}

		// A queued join arriving after handleParticipantsLoaded failed gets the
		// same close code (4012) rather than 4003.
		hFail := newHub(&fakeStore{err: ErrRoomNotOpen}, slog.New(slog.DiscardHandler))
		rFail := newRoom(hFail, "i2")
		hFail.mu.Lock()
		hFail.rooms["i2"] = rFail
		hFail.mu.Unlock()

		cFail := &fakeConn{}
		sFail := hFail.newSession("m1", cFail)
		rFail.post(func() { rFail.handleParticipantsLoaded(ctx, nil, ErrRoomNotOpen) })
		rFail.post(func() { rFail.handleJoin(ctx, sFail) })
		synctest.Wait()

		closed, code, reason := cFail.closeState()
		if !closed || code != closeRoomNotOpen || reason != "ROOM_NOT_OPEN" {
			t.Errorf("queued join after load failure close = (%t, %d, %q), want (true, %d, ROOM_NOT_OPEN)", closed, code, reason, closeRoomNotOpen)
		}
	})
}
