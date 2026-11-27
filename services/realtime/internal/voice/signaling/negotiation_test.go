package signaling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
	"github.com/google/go-cmp/cmp"
)

var errFake = errors.New("fake error")

const testParticipantID = "p1"

// eventKey marks the context that a test passes with each event. The fake
// driver and the send functions reject other contexts, so a lost context fails
// the test.
type eventKey struct{}

func wantConnectionFailed(cause, problem string) protocol.ErrorMessage {
	return protocol.ErrorMessage{
		Reason:   protocol.ErrorReasonConnectionFailed,
		Message:  problem + "; create a new RTCPeerConnection and send voice.join",
		Metadata: map[string]string{"cause": cause},
	}
}

// fakeDriver is both the driver and the only connection it opens.
type fakeDriver struct {
	openErr, offerErr, answerErr, candidateErr, closeErr, subscribeErr error
	// offerSDP, if set, is the SDP of every offer.
	offerSDP string

	offers     int
	answers    []string
	candidates []media.Candidate
	closes     int
	publishers []media.Conn
	// muted has the argument of each SetMuted call.
	muted []bool
	// onFailed is what Open got.
	onFailed func()
	// restartICE is whether the next offer restarts ICE.
	restartICE bool
}

func checkContext(ctx context.Context) error {
	if ctx.Value(eventKey{}) == nil {
		return errors.New("called without the event's context")
	}
	return nil
}

func (d *fakeDriver) Open(ctx context.Context, participantID string, onFailed func()) (media.Conn, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if participantID != testParticipantID {
		return nil, fmt.Errorf("unexpected participant %q", participantID)
	}
	if d.openErr != nil {
		return nil, d.openErr
	}
	d.onFailed = onFailed
	return d, nil
}

func (d *fakeDriver) Close() error {
	d.closes++
	return d.closeErr
}

func (d *fakeDriver) CreateOffer(ctx context.Context) (string, error) {
	if d.offerErr != nil {
		return "", d.offerErr
	}
	if err := checkContext(ctx); err != nil {
		return "", err
	}
	d.offers++
	if d.offerSDP != "" {
		return d.offerSDP, nil
	}
	sdp := fmt.Sprintf("sdp%d", d.offers)
	if d.restartICE {
		sdp += " ice-restart"
		d.restartICE = false
	}
	return sdp, nil
}

func (d *fakeDriver) RestartICE() {
	d.restartICE = true
}

func (d *fakeDriver) ApplyAnswer(ctx context.Context, sdp string) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if d.answerErr != nil {
		return d.answerErr
	}
	d.answers = append(d.answers, sdp)
	return nil
}

func (d *fakeDriver) AddCandidate(ctx context.Context, c media.Candidate) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if len(d.answers) == 0 {
		return errFake
	}
	d.candidates = append(d.candidates, c)
	return d.candidateErr
}

func (d *fakeDriver) Subscribe(ctx context.Context, publisher media.Conn) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if d.subscribeErr != nil {
		return d.subscribeErr
	}
	d.publishers = append(d.publishers, publisher)
	return nil
}

func (d *fakeDriver) SetMuted(muted bool) {
	d.muted = append(d.muted, muted)
}

type harness struct {
	n      *negotiation
	driver *fakeDriver
	sent   []protocol.ServerMessage
	posted chan func()
}

func eventContext(t *testing.T) context.Context {
	return context.WithValue(t.Context(), eventKey{}, true)
}

// startNegotiation creates a negotiation on d and sends the first offer. Tests
// that send an offer call it inside a synctest bubble so that the answer timer
// uses the bubble's fake clock.
func startNegotiation(ctx context.Context, d *fakeDriver) *harness {
	h := &harness{driver: d, posted: make(chan func())}
	h.n = newNegotiation(
		d,
		func(ctx context.Context, msg protocol.ServerMessage) {
			if ctx.Value(eventKey{}) != nil {
				h.sent = append(h.sent, msg)
			}
		},
		func(f func()) { h.posted <- f },
		slog.New(slog.DiscardHandler),
	)
	h.n.renegotiationNeeded(ctx)
	return h
}

func TestNegotiationCreateOfferError(t *testing.T) {
	ctx := eventContext(t)
	h := startNegotiation(ctx, &fakeDriver{offerErr: errFake})

	want := []protocol.ServerMessage{wantConnectionFailed("create-offer", "the server could not create an offer")}
	if diff := cmp.Diff(want, h.sent); diff != "" {
		t.Errorf("renegotiationNeeded() sent messages diff (-want +got):\n%s", diff)
	}
	if h.n.state != stateClosed {
		t.Errorf("renegotiationNeeded() state = %q, want %q", h.n.state, stateClosed)
	}
	if h.driver.closes != 1 {
		t.Errorf("renegotiationNeeded() closed the connection %d times, want 1", h.driver.closes)
	}
}

func TestNegotiationAddsPendingCandidatesAfterAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{})
		h.n.receiveCandidate(ctx, protocol.Candidate{Candidate: "c1"})

		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})

		want := []media.Candidate{{Candidate: "c1"}}
		if diff := cmp.Diff(want, h.driver.candidates); diff != "" {
			t.Fatalf("receiveAnswer(offerId 1) added candidates diff (-want +got):\n%s", diff)
		}

		h.n.renegotiationNeeded(ctx)
		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 2})
		if diff := cmp.Diff(want, h.driver.candidates); diff != "" {
			t.Errorf("receiveAnswer(offerId 2) added candidates diff (-want +got):\n%s", diff)
		}
	})
}

func TestNegotiationStableState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// candidateErr only drives the warning log path.
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{candidateErr: errFake})
		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})

		h.n.receiveCandidate(ctx, protocol.Candidate{Candidate: "c1"})

		want := []media.Candidate{{Candidate: "c1"}}
		if diff := cmp.Diff(want, h.driver.candidates); diff != "" {
			t.Errorf("receiveCandidate(c1) added candidates diff (-want +got):\n%s", diff)
		}

		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})
		if diff := cmp.Diff([]string{"answer"}, h.driver.answers); diff != "" {
			t.Errorf("repeated receiveAnswer(offerId 1) applied answers diff (-want +got):\n%s", diff)
		}

		answeredOfferTimeout := <-h.posted
		answeredOfferTimeout()
		if diff := cmp.Diff([]protocol.ServerMessage{protocol.Offer{SDP: "sdp1", OfferID: 1}}, h.sent); diff != "" {
			t.Errorf("after the timer of the answered offer, sent messages diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateStable {
			t.Errorf("after the timer of the answered offer, state = %q, want %q", h.n.state, stateStable)
		}
	})
}

func TestNegotiationRenegotiationPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{})
		h.n.renegotiationNeeded(ctx)

		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer1", OfferID: 1})

		if diff := cmp.Diff([]string{"answer1"}, h.driver.answers); diff != "" {
			t.Errorf("receiveAnswer(offerId 1) applied answers diff (-want +got):\n%s", diff)
		}
		want := []protocol.ServerMessage{
			protocol.Offer{SDP: "sdp1", OfferID: 1},
			protocol.Offer{SDP: "sdp2", OfferID: 2},
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receiveAnswer(offerId 1) sent messages diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateAwaitingAnswer {
			t.Fatalf("receiveAnswer(offerId 1) state = %q, want %q", h.n.state, stateAwaitingAnswer)
		}

		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer1", OfferID: 1})
		if diff := cmp.Diff([]string{"answer1"}, h.driver.answers); diff != "" {
			t.Errorf("repeated receiveAnswer(offerId 1) applied answers diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateAwaitingAnswer {
			t.Errorf("repeated receiveAnswer(offerId 1) state = %q, want %q", h.n.state, stateAwaitingAnswer)
		}
	})
}

func TestNegotiationApplyAnswerError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{answerErr: errFake})

		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})

		want := []protocol.ServerMessage{
			protocol.Offer{SDP: "sdp1", OfferID: 1},
			wantConnectionFailed("apply-answer", "the server could not apply voice.answer"),
		}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("receiveAnswer(offerId 1) sent messages diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateClosed {
			t.Errorf("receiveAnswer(offerId 1) state = %q, want %q", h.n.state, stateClosed)
		}
	})
}

func TestNegotiationTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{})
		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})
		// Sending offer 2 half a timeout later makes the two timers fire apart.
		time.Sleep(answerTimeout / 2)
		h.n.renegotiationNeeded(ctx)
		offers := []protocol.ServerMessage{
			protocol.Offer{SDP: "sdp1", OfferID: 1},
			protocol.Offer{SDP: "sdp2", OfferID: 2},
		}
		if diff := cmp.Diff(offers, h.sent); diff != "" {
			t.Fatalf("renegotiationNeeded() sent messages diff (-want +got):\n%s", diff)
		}

		// Receiving blocks the bubble, so the fake clock jumps to the next timer.
		answeredOfferTimeout := <-h.posted
		answeredOfferTimeout()
		if h.n.state != stateAwaitingAnswer {
			t.Fatalf("after the timer of offer 1, state = %q, want %q", h.n.state, stateAwaitingAnswer)
		}

		awaitedOfferTimeout := <-h.posted
		awaitedOfferTimeout()
		want := []protocol.ServerMessage{offers[0], offers[1], wantConnectionFailed("answer-timeout", "no voice.answer arrived in time")}
		if diff := cmp.Diff(want, h.sent); diff != "" {
			t.Errorf("after the timer of offer 2, sent messages diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateClosed {
			t.Errorf("after the timer of offer 2, state = %q, want %q", h.n.state, stateClosed)
		}
	})
}

func TestNegotiationLeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// closeErr only drives the warning log path.
		ctx := eventContext(t)
		h := startNegotiation(ctx, &fakeDriver{closeErr: errFake})
		h.n.receiveAnswer(ctx, protocol.Answer{SDP: "answer", OfferID: 1})

		h.n.leave(ctx)
		if h.n.state != stateClosed {
			t.Fatalf("leave() state = %q, want %q", h.n.state, stateClosed)
		}

		h.n.leave(ctx)
		h.n.renegotiationNeeded(ctx)
		h.n.receiveCandidate(ctx, protocol.Candidate{Candidate: "c1"})

		if diff := cmp.Diff([]protocol.ServerMessage{protocol.Offer{SDP: "sdp1", OfferID: 1}}, h.sent); diff != "" {
			t.Errorf("events after leave() sent messages diff (-want +got):\n%s", diff)
		}
		if h.driver.closes != 1 {
			t.Errorf("leave() twice closed the connection %d times, want 1", h.driver.closes)
		}
		if diff := cmp.Diff([]media.Candidate(nil), h.driver.candidates); diff != "" {
			t.Errorf("receiveCandidate(c1) after leave() added candidates diff (-want +got):\n%s", diff)
		}
		if h.n.state != stateClosed {
			t.Errorf("events after leave() state = %q, want %q", h.n.state, stateClosed)
		}
	})
}
