package signaling

import (
	"context"
	"errors"
	"log/slog"
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

// newRoomHarness creates a room on d. Tests call it inside a synctest bubble so
// that they can wait for the room goroutine.
func newRoomHarness(d *fakeDriver) *roomHarness {
	h := &roomHarness{driver: d}
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

		if diff := cmp.Diff([]sentMessage(nil), h.sent); diff != "" {
			t.Errorf("receive(answer, candidate) before join sent messages diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string(nil), h.driver.answers); diff != "" {
			t.Errorf("receive(answer, candidate) before join applied answers diff (-want +got):\n%s", diff)
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
