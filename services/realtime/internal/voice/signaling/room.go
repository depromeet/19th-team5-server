package signaling

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
)

// room runs the signaling of one room. It handles client messages and timers
// one at a time on the room goroutine.
type room struct {
	driver media.Driver
	// send sends msg to a participant. It must not block, because the room
	// goroutine calls it.
	send   func(ctx context.Context, participantID string, msg []byte)
	logger *slog.Logger

	mu    sync.Mutex
	queue []func()
	busy  bool

	// negotiations is keyed by participant ID. Only the room goroutine uses it.
	negotiations map[string]*negotiation
}

func newRoom(driver media.Driver, send func(ctx context.Context, participantID string, msg []byte), logger *slog.Logger) *room {
	return &room{
		driver:       driver,
		send:         send,
		logger:       logger,
		negotiations: make(map[string]*negotiation),
	}
}

// receive handles a client message on the room goroutine. It takes ownership
// of data, and the caller should not use data after this call. It is safe to
// call from multiple goroutines. It returns before the message is handled, and
// the message is handled even if ctx is cancelled.
func (r *room) receive(ctx context.Context, participantID string, data []byte) {
	ctx = context.WithoutCancel(ctx)
	r.post(func() { r.handle(ctx, participantID, data) })
}

// post runs f on the room goroutine after the functions posted before it, and
// never blocks. It is safe to call from multiple goroutines. The room goroutine
// starts when f is posted to an empty queue and exits when the queue is empty
// again.
func (r *room) post(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = append(r.queue, f)
	if !r.busy {
		r.busy = true
		go r.run()
	}
}

func (r *room) run() {
	for {
		r.mu.Lock()
		fs := r.queue
		r.queue = nil
		if len(fs) == 0 {
			r.busy = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		for _, f := range fs {
			f()
		}
	}
}

func (r *room) handle(ctx context.Context, participantID string, data []byte) {
	msg, err := protocol.Parse(data)
	if err != nil {
		var parseErr *protocol.ParseError
		// Parse returns only a *ParseError.
		errors.As(err, &parseErr)
		r.sendMessage(ctx, participantID, parseErr.ErrorMessage())
		return
	}
	switch msg := msg.(type) {
	case protocol.Join:
		r.join(ctx, participantID)
	case protocol.Answer:
		if n, ok := r.negotiations[participantID]; ok {
			n.receiveAnswer(ctx, msg)
		}
	case protocol.Candidate:
		if n, ok := r.negotiations[participantID]; ok {
			n.receiveCandidate(ctx, msg)
		}
	case protocol.RestartICE:
		if n, ok := r.negotiations[participantID]; ok {
			n.restartICE(ctx)
		}
	case protocol.SetMuted:
		r.setMuted(ctx, participantID, msg.Muted)
	case protocol.Leave:
		r.leave(ctx, participantID)
	}
}

func (r *room) join(ctx context.Context, participantID string) {
	r.leave(ctx, participantID)
	logger := r.logger.With("participantId", participantID)
	var n *negotiation
	conn, err := r.driver.Open(ctx, participantID, func() {
		// The posted function runs on the room goroutine after join returns,
		// so n is set by then. If the participant has left or joined again
		// since, n is closed.
		r.post(func() { n.restartICE(ctx) })
	})
	if err != nil {
		logger.WarnContext(ctx, "open connection failed", "err", err)
		r.sendMessage(ctx, participantID, connectionFailed("open-connection", "the server could not open the voice connection"))
		return
	}
	n = newNegotiation(
		conn,
		func(ctx context.Context, msg protocol.ServerMessage) { r.sendMessage(ctx, participantID, msg) },
		r.post,
		logger,
	)
	for otherID, other := range r.negotiations {
		if other.state == stateClosed {
			continue
		}
		subscribe(ctx, n, other)
		subscribe(ctx, other, n)
		other.renegotiationNeeded(ctx)
		// A new participant takes the others to be unmuted.
		if other.muted {
			r.sendMessage(ctx, participantID, protocol.Muted{ParticipantID: otherID, Muted: true})
		}
	}
	r.negotiations[participantID] = n
	n.renegotiationNeeded(ctx)
}

func subscribe(ctx context.Context, subscriber, publisher *negotiation) {
	if err := subscriber.conn.Subscribe(ctx, publisher.conn); err != nil {
		subscriber.logger.WarnContext(ctx, "subscribe failed", "err", err)
	}
}

func (r *room) leave(ctx context.Context, participantID string) {
	n, ok := r.negotiations[participantID]
	if !ok {
		return
	}
	n.leave(ctx)
	delete(r.negotiations, participantID)
	if n.muted {
		// The participant is unmuted when they join again.
		r.sendOthers(ctx, participantID, protocol.Muted{ParticipantID: participantID, Muted: false})
	}
	for _, other := range r.negotiations {
		other.renegotiationNeeded(ctx)
	}
}

// setMuted mutes or unmutes the participant's voice, and tells the others if
// that changes it.
func (r *room) setMuted(ctx context.Context, participantID string, muted bool) {
	n, ok := r.negotiations[participantID]
	// A closed connection must not be used.
	if !ok || n.state == stateClosed || n.muted == muted {
		return
	}
	n.conn.SetMuted(muted)
	n.muted = muted
	r.sendOthers(ctx, participantID, protocol.Muted{ParticipantID: participantID, Muted: muted})
}

// sendOthers sends msg to every participant but participantID.
func (r *room) sendOthers(ctx context.Context, participantID string, msg protocol.ServerMessage) {
	for id := range r.negotiations {
		if id != participantID {
			r.sendMessage(ctx, id, msg)
		}
	}
}

func (r *room) sendMessage(ctx context.Context, participantID string, msg protocol.ServerMessage) {
	data, err := protocol.Marshal(msg)
	if err != nil {
		r.logger.WarnContext(ctx, "marshal message failed", "participantId", participantID, "err", err)
		return
	}
	r.send(ctx, participantID, data)
}
