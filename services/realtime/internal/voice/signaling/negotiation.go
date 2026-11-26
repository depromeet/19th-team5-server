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

type negotiation struct {
	participantID string
	engine        media.Engine
	send          func(context.Context, protocol.ServerMessage)
	// post runs a function on the room goroutine. Answer timers call it on
	// their own goroutines, even after the negotiation has closed.
	post   func(func())
	logger *slog.Logger

	conn              media.Conn
	state             negotiationState
	offerID           int
	pendingCandidates []media.Candidate
}

func newNegotiation(
	participantID string,
	engine media.Engine,
	send func(context.Context, protocol.ServerMessage),
	post func(func()),
	logger *slog.Logger,
) *negotiation {
	return &negotiation{
		participantID: participantID,
		engine:        engine,
		send:          send,
		post:          post,
		logger:        logger.With("participantId", participantID),
	}
}

// start opens the connection and sends the first offer. It must be called once,
// before any other event.
func (n *negotiation) start(ctx context.Context) {
	conn, err := n.engine.Open(ctx, n.participantID)
	if err != nil {
		n.logger.WarnContext(ctx, "open connection failed", "err", err)
		n.state = stateClosed
		n.sendConnectionFailed(ctx, "the server could not open the voice connection")
		return
	}
	n.conn = conn
	n.sendOffer(ctx)
}

func (n *negotiation) renegotiationNeeded(ctx context.Context) {
	switch n.state {
	case stateStable:
		n.sendOffer(ctx)
	case stateAwaitingAnswer:
		n.state = stateRenegotiationPending
	}
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
		n.fail(ctx, "the server could not apply voice.answer")
		return
	}
	for _, c := range n.pendingCandidates {
		n.addCandidate(ctx, c)
	}
	n.pendingCandidates = nil
	if n.state == stateRenegotiationPending {
		n.sendOffer(ctx)
		return
	}
	n.state = stateStable
}

func (n *negotiation) timeout(ctx context.Context, offerID int) {
	if !n.awaits(offerID) {
		return
	}
	n.logger.WarnContext(ctx, "answer timed out", "offerId", offerID)
	n.fail(ctx, "no voice.answer arrived in time")
}

func (n *negotiation) leave(ctx context.Context) {
	if n.state == stateClosed {
		return
	}
	n.close(ctx)
}

func (n *negotiation) awaits(offerID int) bool {
	return (n.state == stateAwaitingAnswer || n.state == stateRenegotiationPending) && offerID == n.offerID
}

func (n *negotiation) sendOffer(ctx context.Context) {
	n.offerID++
	offerID := n.offerID
	sdp, err := n.conn.CreateOffer(ctx)
	if err != nil {
		n.logger.WarnContext(ctx, "create offer failed", "offerId", offerID, "err", err)
		n.fail(ctx, "the server could not create an offer")
		return
	}
	n.state = stateAwaitingAnswer
	timerCtx := context.WithoutCancel(ctx)
	time.AfterFunc(answerTimeout, func() {
		n.post(func() { n.timeout(timerCtx, offerID) })
	})
	n.send(ctx, protocol.Offer{SDP: sdp, OfferID: offerID})
}

func (n *negotiation) fail(ctx context.Context, problem string) {
	n.close(ctx)
	n.sendConnectionFailed(ctx, problem)
}

func (n *negotiation) sendConnectionFailed(ctx context.Context, problem string) {
	n.send(ctx, protocol.ErrorMessage{
		Reason:  protocol.ErrorReasonConnectionFailed,
		Message: problem + "; create a new RTCPeerConnection and send voice.join",
	})
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
	n.state = stateClosed
}
