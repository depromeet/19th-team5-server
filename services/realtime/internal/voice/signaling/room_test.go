package signaling

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
	"github.com/google/go-cmp/cmp"
)

type sentMessage struct {
	ParticipantID string
	Data          string
}

type roomHarness struct {
	r      *room
	driver *fakeDriver
	sent   []sentMessage
}

// fakeDrivers opens the connection of each participant on its own fakeDriver.
type fakeDrivers map[string]*fakeDriver

func (d fakeDrivers) Open(ctx context.Context, participantID string, _ func()) (media.Conn, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return d[participantID], nil
}

// newRoomHarness creates a room on d. Tests call it inside a synctest bubble so
// that they can wait for the room goroutine.
func newRoomHarness(d *fakeDriver) *roomHarness {
	h := newMultiRoomHarness(d)
	h.driver = d
	return h
}

func newMultiRoomHarness(d media.Driver) *roomHarness {
	h := &roomHarness{}
	h.r = newRoom(
		d,
		func(ctx context.Context, participantID string, msg []byte) {
			if ctx.Value(eventKey{}) != nil {
				h.sent = append(h.sent, sentMessage{ParticipantID: participantID, Data: string(msg)})
			}
		},
		slog.New(slog.DiscardHandler),
	)
	return h
}

// receive passes data to the room and waits until the room goroutine has
// handled it.
func (h *roomHarness) receive(ctx context.Context, participantID, data string) {
	h.r.receive(ctx, participantID, []byte(data))
	synctest.Wait()
}

func message(t *testing.T, participantID string, msg protocol.ServerMessage) sentMessage {
	t.Helper()
	data, err := protocol.Marshal(msg)
	if err != nil {
		t.Fatalf("protocol.Marshal(%#v) failed: %v", msg, err)
	}
	return sentMessage{ParticipantID: participantID, Data: string(data)}
}

func TestRoomPostWaitsForRunningFunction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRoom(&fakeDriver{}, nil, slog.New(slog.DiscardHandler))
		release := make(chan struct{})
		var ran []string

		r.post(func() {
			<-release
			ran = append(ran, "first")
		})
		synctest.Wait()
		r.post(func() { ran = append(ran, "second") })
		synctest.Wait()
		close(release)
		synctest.Wait()

		if diff := cmp.Diff([]string{"first", "second"}, ran); diff != "" {
			t.Errorf("post() run order diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomRoutesMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{})

		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.answer","payload":{"sdp":"answer","offerId":1}}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.candidate","payload":{"candidate":"c1","sdpMid":"0"}}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.leave"}`)

		want := []sentMessage{message(t, testParticipantID, protocol.Offer{SDP: "sdp1", OfferID: 1})}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(join, answer, candidate, leave) sent messages diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"answer"}, h.driver.answers); diff != "" {
			t.Errorf("receive(join, answer, candidate, leave) applied answers diff (-want +got):\n%s", diff)
		}
		wantCandidates := []media.Candidate{{Candidate: "c1", SDPMid: new("0")}}
		if diff := cmp.Diff(wantCandidates, h.driver.candidates); diff != "" {
			t.Errorf("receive(join, answer, candidate, leave) added candidates diff (-want +got):\n%s", diff)
		}
		if h.driver.closes != 1 {
			t.Errorf("voice.leave closed the connection %d times, want 1", h.driver.closes)
		}
		if len(h.r.negotiations) != 0 {
			t.Errorf("after voice.leave, the room has %d negotiations, want 0", len(h.r.negotiations))
		}
	})
}

func TestRoomRejoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{})

		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)

		want := []sentMessage{
			message(t, testParticipantID, protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, testParticipantID, protocol.Offer{SDP: "sdp2", OfferID: 1}),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(join, join) sent messages diff (-want +got):\n%s", diff)
		}
		if h.driver.closes != 1 {
			t.Errorf("the second voice.join closed the connection %d times, want 1", h.driver.closes)
		}
	})
}

func TestRoomIgnoresMessagesBeforeJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{})

		h.receive(ctx, testParticipantID, `{"type":"voice.answer","payload":{"sdp":"answer","offerId":1}}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.candidate","payload":{"candidate":"c1","sdpMid":"0"}}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.setMuted","payload":{"muted":true}}`)

		if diff := cmp.Diff([]sentMessage(nil), h.sent); diff != "" {
			t.Errorf("receive(answer, candidate, setMuted) before join sent messages diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string(nil), h.driver.answers); diff != "" {
			t.Errorf("receive(answer, candidate, setMuted) before join applied answers diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]bool(nil), h.driver.muted); diff != "" {
			t.Errorf("receive(answer, candidate, setMuted) before join SetMuted calls diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomInvalidMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{})

		const in = `{"type":"voice.unknown"}`
		_, err := protocol.Parse([]byte(in))
		var parseErr *protocol.ParseError
		if !errors.As(err, &parseErr) {
			t.Fatalf("Parse(%s) error = %v, want a *ParseError", in, err)
		}

		h.receive(ctx, testParticipantID, in)

		want := []sentMessage{
			message(t, testParticipantID, parseErr.ErrorMessage()),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(%s) sent messages diff (-want +got):\n%s", in, diff)
		}
	})
}

func TestRoomOpenError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{openErr: errFake})

		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)

		want := []sentMessage{
			message(t, testParticipantID, wantConnectionFailed("open-connection", "the server could not open the voice connection")),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(join) with a failing Open sent messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomMarshalError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		// encoding/json/v2 rejects invalid UTF-8.
		h := newRoomHarness(&fakeDriver{offerSDP: "\xff"})

		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)

		if diff := cmp.Diff([]sentMessage(nil), h.sent); diff != "" {
			t.Errorf("receive(join) with an unmarshalable offer sent messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomJoinAndLeaveRenegotiateOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		// subscribeErr only drives the warning log path.
		p1, p2 := &fakeDriver{subscribeErr: errFake}, &fakeDriver{}
		h := newMultiRoomHarness(fakeDrivers{"p1": p1, "p2": p2})
		h.receive(ctx, "p1", `{"type":"voice.join"}`)
		h.receive(ctx, "p1", `{"type":"voice.answer","payload":{"sdp":"answer","offerId":1}}`)

		h.receive(ctx, "p2", `{"type":"voice.join"}`)

		if !slices.Equal(p2.publishers, []media.Conn{p1}) {
			t.Errorf("after p2 joined, p2 subscribed to %v, want only p1", p2.publishers)
		}
		want := []sentMessage{
			message(t, "p1", protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, "p1", protocol.Offer{SDP: "sdp2", OfferID: 2}),
			message(t, "p2", protocol.Offer{SDP: "sdp1", OfferID: 1}),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Fatalf("after p2 joined, sent messages diff (-want +got):\n%s", diff)
		}

		h.receive(ctx, "p1", `{"type":"voice.answer","payload":{"sdp":"answer","offerId":2}}`)
		h.receive(ctx, "p2", `{"type":"voice.leave"}`)

		want = append(want, message(t, "p1", protocol.Offer{SDP: "sdp3", OfferID: 3}))
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("after p2 left, sent messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomJoinSkipsClosedNegotiation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		p1, p2 := &fakeDriver{answerErr: errFake}, &fakeDriver{}
		h := newMultiRoomHarness(fakeDrivers{"p1": p1, "p2": p2})
		h.receive(ctx, "p1", `{"type":"voice.join"}`)
		h.receive(ctx, "p1", `{"type":"voice.answer","payload":{"sdp":"answer","offerId":1}}`)

		h.receive(ctx, "p2", `{"type":"voice.join"}`)

		if len(p1.publishers) != 0 || len(p2.publishers) != 0 {
			t.Errorf("receive(p2 join) after p1's answer failed: p1 subscribed to %v and p2 to %v, want none", p1.publishers, p2.publishers)
		}
	})
}

func TestRoomSetMuted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		p1, p2 := &fakeDriver{}, &fakeDriver{}
		h := newMultiRoomHarness(fakeDrivers{"p1": p1, "p2": p2})
		h.receive(ctx, "p1", `{"type":"voice.join"}`)
		h.receive(ctx, "p2", `{"type":"voice.join"}`)

		h.receive(ctx, "p1", `{"type":"voice.setMuted","payload":{"muted":true}}`)
		h.receive(ctx, "p1", `{"type":"voice.setMuted","payload":{"muted":true}}`)

		if diff := cmp.Diff([]bool{true}, p1.muted); diff != "" {
			t.Errorf("receive(setMuted true, setMuted true) SetMuted calls diff (-want +got):\n%s", diff)
		}
		want := []sentMessage{
			message(t, "p1", protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, "p2", protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, "p2", protocol.Muted{ParticipantID: "p1", Muted: true}),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(setMuted true, setMuted true) sent messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomSetMutedIgnoresClosedNegotiation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newRoomHarness(&fakeDriver{answerErr: errFake})
		h.receive(ctx, testParticipantID, `{"type":"voice.join"}`)
		h.receive(ctx, testParticipantID, `{"type":"voice.answer","payload":{"sdp":"answer","offerId":1}}`)

		h.receive(ctx, testParticipantID, `{"type":"voice.setMuted","payload":{"muted":true}}`)

		if diff := cmp.Diff([]bool(nil), h.driver.muted); diff != "" {
			t.Errorf("receive(setMuted true) after the answer failed, SetMuted calls diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomJoinSendsMutedStates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newMultiRoomHarness(fakeDrivers{"p1": {}, "p2": {}, "p3": {}})
		h.receive(ctx, "p1", `{"type":"voice.join"}`)
		h.receive(ctx, "p2", `{"type":"voice.join"}`)
		h.receive(ctx, "p1", `{"type":"voice.setMuted","payload":{"muted":true}}`)

		h.receive(ctx, "p3", `{"type":"voice.join"}`)

		var got []sentMessage
		for _, m := range h.sent {
			if m.ParticipantID == "p3" {
				got = append(got, m)
			}
		}
		want := []sentMessage{
			message(t, "p3", protocol.Muted{ParticipantID: "p1", Muted: true}),
			message(t, "p3", protocol.Offer{SDP: "sdp1", OfferID: 1}),
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("receive(p3 join) sent p3 messages diff (-want +got):\n%s", diff)
		}
	})
}

func TestRoomLeaveUnmutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := newMultiRoomHarness(fakeDrivers{"p1": {}, "p2": {}})
		h.receive(ctx, "p1", `{"type":"voice.join"}`)
		h.receive(ctx, "p2", `{"type":"voice.join"}`)
		h.receive(ctx, "p1", `{"type":"voice.setMuted","payload":{"muted":true}}`)

		h.receive(ctx, "p1", `{"type":"voice.leave"}`)

		want := []sentMessage{
			message(t, "p1", protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, "p2", protocol.Offer{SDP: "sdp1", OfferID: 1}),
			message(t, "p2", protocol.Muted{ParticipantID: "p1", Muted: true}),
			message(t, "p2", protocol.Muted{ParticipantID: "p1", Muted: false}),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receive(p1 leave) sent messages diff (-want +got):\n%s", diff)
		}
	})
}
