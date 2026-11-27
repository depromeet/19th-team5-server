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
			name:  "invalid JSON",
			input: `{`,
			cause: causeInvalidJSON,
		},
		{
			name:  "invalid UTF-8",
			input: "{\"type\":\"room.join\",\"payload\":{\"interviewId\":\"\xff\"}}",
			cause: causeInvalidJSON,
		},
		{
			name:  "duplicate field",
			input: `{"type":"room.join","type":"room.leave"}`,
			cause: causeDuplicateField,
		},
		{
			name:  "payload not object",
			input: `{"type":"room.join","payload":[]}`,
			cause: causePayloadNotObject,
			field: "/payload",
		},
		{
			name:  "unknown type",
			input: `{"type":"room.unknown"}`,
			cause: causeUnknownType,
			field: "/type",
		},
		{
			name:  "unknown feature prefix",
			input: `{"type":"chat.send"}`,
			cause: causeUnknownType,
			field: "/type",
		},
		{
			name:  "wrong field type",
			input: `{"type":"room.join","payload":{"interviewId":123}}`,
			cause: causeWrongFieldType,
			field: "/payload/interviewId",
		},
		{
			name:  "case sensitive field name treated as missing",
			input: `{"type":"room.join","payload":{"InterviewId":"i1"}}`,
			cause: causeMissingField,
			field: "/payload/interviewId",
		},
		{
			name:  "null payload on room.join is missing field",
			input: `{"type":"room.join","payload":null}`,
			cause: causeMissingField,
			field: "/payload/interviewId",
		},
		{
			name:  "room.leave before join",
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
			t.Errorf("messages diff (-want +got):\n%s", diff)
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
			name:       "forbidden member",
			memberID:   "outsider",
			wantCode:   closeForbidden,
			wantReason: "FORBIDDEN",
		},
		{
			name:       "room not open",
			storeErr:   ErrRoomNotOpen,
			memberID:   "m1",
			wantCode:   closeRoomNotOpen,
			wantReason: "ROOM_NOT_OPEN",
		},
		{
			name:       "store failure",
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
					t.Errorf("closeState() = (%t, %d, %q), want (true, %d, %q)", closed, code, reason, tc.wantCode, tc.wantReason)
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
