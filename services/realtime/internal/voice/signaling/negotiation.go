// Package signaling runs the voice signaling of each room.
package signaling

import (
	"context"
	"log/slog"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
)

const answerTimeout = 10 * time.Second

type negotiationState string

const (
	stateStable               negotiationState = "stable"
	stateAwaitingAnswer       negotiationState = "awaitingAnswer"
	stateRenegotiationPending negotiationState = "renegotiationPending"
	stateClosed               negotiationState = "closed"
)

// negotiation runs the offers and answers of one participant's connection on
// the room goroutine. The server makes every offer. An answer or a timeout for
// any offer but the latest is ignored, and candidates that arrive before the
// answer wait until it is applied. Once closed, it ignores every event.
type negotiation struct {
	conn media.Conn
	send func(context.Context, protocol.ServerMessage)
	// post runs a function on the room goroutine. Answer timers call it on
	// their own goroutines, even after the negotiation has closed.
	post   func(func())
	logger *slog.Logger

	state             negotiationState
	connected         bool
	offerID           int
	pendingOffer      string
	pendingCandidates []media.Candidate

	// muted is whether the participant's voice is muted. It is not part of the
	// negotiation, and only the room uses it.
	muted bool
}

func newNegotiation(conn media.Conn, send func(context.Context, protocol.ServerMessage), post func(func()), logger *slog.Logger) *negotiation {
	return &negotiation{
		conn:      conn,
		send:      send,
		post:      post,
		logger:    logger,
		state:     stateStable,
		connected: true,
	}
}

// renegotiationNeeded sends a new offer now, or after the answer that n awaits.
func (n *negotiation) renegotiationNeeded(ctx context.Context) {
	switch n.state {
	case stateStable:
		n.sendOffer(ctx)
	case stateAwaitingAnswer:
		n.state = stateRenegotiationPending
	}
}

// restartICE sends an offer that restarts ICE now, or after the answer that n
// awaits.
func (n *negotiation) restartICE(ctx context.Context) {
	// A closed connection must not be used.
	if n.state == stateClosed {
		return
	}
	n.conn.RestartICE()
	n.renegotiationNeeded(ctx)
}

func (n *negotiation) receiveCandidate(ctx context.Context, c protocol.Candidate) {
	switch n.state {
	case stateStable:
		n.addCandidate(ctx, media.Candidate(c))
	case stateAwaitingAnswer, stateRenegotiationPending:
		n.pendingCandidates = append(n.pendingCandidates, media.Candidate(c))
	}
}

func (n *negotiation) receiveAnswer(ctx context.Context, a protocol.Answer) {
	if !n.awaits(a.OfferID) {
		return
	}
	if err := n.conn.ApplyAnswer(ctx, a.SDP); err != nil {
		n.logger.WarnContext(ctx, "apply answer failed", "offerId", a.OfferID, "err", err)
		n.fail(ctx, "apply-answer", "the server could not apply voice.answer")
		return
	}
	for _, c := range n.pendingCandidates {
		n.addCandidate(ctx, c)
	}
	n.pendingCandidates = nil
	n.pendingOffer = ""
	if n.state == stateRenegotiationPending {
		n.sendOffer(ctx)
		return
	}
	n.state = stateStable
}

func (n *negotiation) timeout(ctx context.Context, offerID int) {
	if !n.awaits(offerID) || !n.connected {
		return
	}
	n.logger.WarnContext(ctx, "answer timed out", "offerId", offerID)
	n.fail(ctx, "answer-timeout", "no voice.answer arrived in time")
}

func (n *negotiation) disconnect() {
	if n.state == stateClosed {
		return
	}
	n.connected = false
}

func (n *negotiation) reconnect(ctx context.Context) {
	if n.state == stateClosed {
		return
	}
	n.connected = true
	if n.state == stateAwaitingAnswer || n.state == stateRenegotiationPending {
		n.dispatchOffer(ctx, n.pendingOffer)
	}
}

func (n *negotiation) leave(ctx context.Context) {
	if n.state == stateClosed {
		return
	}
	n.close(ctx)
}

// awaits reports whether n is waiting for the answer to offerID.
func (n *negotiation) awaits(offerID int) bool {
	return (n.state == stateAwaitingAnswer || n.state == stateRenegotiationPending) && offerID == n.offerID
}

func (n *negotiation) sendOffer(ctx context.Context) {
	sdp, err := n.conn.CreateOffer(ctx)
	if err != nil {
		n.logger.WarnContext(ctx, "create offer failed", "offerId", n.offerID+1, "err", err)
		n.fail(ctx, "create-offer", "the server could not create an offer")
		return
	}
	n.state = stateAwaitingAnswer
	n.pendingOffer = sdp
	n.dispatchOffer(ctx, sdp)
}

func (n *negotiation) dispatchOffer(ctx context.Context, sdp string) {
	n.offerID++
	offerID := n.offerID
	timerCtx := context.WithoutCancel(ctx)
	time.AfterFunc(answerTimeout, func() {
		n.post(func() { n.timeout(timerCtx, offerID) })
	})
	n.send(ctx, protocol.Offer{SDP: sdp, OfferID: offerID})
}

func (n *negotiation) fail(ctx context.Context, cause, problem string) {
	n.close(ctx)
	n.send(ctx, connectionFailed(cause, problem))
}

// connectionFailed returns a CONNECTION_FAILED error. cause names the failed
// step in the metadata, and problem describes it in the message.
func connectionFailed(cause, problem string) protocol.ErrorMessage {
	return protocol.ErrorMessage{
		Reason:   protocol.ErrorReasonConnectionFailed,
		Message:  problem + "; create a new RTCPeerConnection and send voice.join",
		Metadata: map[string]string{"cause": cause},
	}
}

func (n *negotiation) addCandidate(ctx context.Context, c media.Candidate) {
	if err := n.conn.AddCandidate(ctx, c); err != nil {
		n.logger.WarnContext(ctx, "add candidate failed", "err", err)
	}
}

func (n *negotiation) close(ctx context.Context) {
	if err := n.conn.Close(); err != nil {
		n.logger.WarnContext(ctx, "close connection failed", "err", err)
	}
	n.pendingOffer = ""
	n.state = stateClosed
}
