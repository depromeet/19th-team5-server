package room

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	defaultPingInterval = 10 * time.Second
	sendQueueSize       = 64
)

// TicketVerifier verifies a one-time WebSocket ticket and returns the
// authenticated member ID.
type TicketVerifier interface {
	Verify(ctx context.Context, ticket string) (memberID string, err error)
}

// Options configures a [Server]. Zero values use defaults.
type Options struct {
	Logger         *slog.Logger
	OriginPatterns []string
	// NewVoiceRoom creates a voice room for each interview room. If nil,
	// voice.* messages are rejected as unknown-type.
	NewVoiceRoom VoiceRoomFactory
	// PingInterval is how often the server sends WebSocket pings. Defaults to 10s.
	PingInterval time.Duration
	// GracePeriod is how long a disconnected participant stays in RECONNECTING
	// before leaving. Defaults to 30s.
	GracePeriod time.Duration
}

// Server serves WebSocket connections for interview rooms.
type Server struct {
	verifier       TicketVerifier
	hub            *hub
	logger         *slog.Logger
	originPatterns []string
	pingInterval   time.Duration

	mu     sync.Mutex
	closed bool
	conns  map[*wsConn]struct{}
	wg     sync.WaitGroup
}

// NewServer creates a [Server] backed by verifier and store.
func NewServer(verifier TicketVerifier, store ParticipantStore, opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	h := newHub(store, logger)
	h.newVoiceRoom = opts.NewVoiceRoom
	if opts.GracePeriod > 0 {
		h.gracePeriod = opts.GracePeriod
	}
	pingInterval := opts.PingInterval
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &Server{
		verifier:       verifier,
		hub:            h,
		logger:         logger,
		originPatterns: opts.OriginPatterns,
		pingInterval:   pingInterval,
		conns:          make(map[*wsConn]struct{}),
	}
}

// ServeHTTP verifies the ticket query parameter, upgrades the request to a
// WebSocket connection, and runs the session until it closes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	memberID, err := s.verifier.Verify(r.Context(), ticket)
	if err != nil {
		// Never log the query string because it carries the ticket.
		s.logger.WarnContext(r.Context(), "ticket verification failed", "path", r.URL.Path, "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.originPatterns,
	})
	if err != nil {
		return
	}

	// Do not use r.Context() after Accept returns (see http.Hijacker).
	connCtx, connCancel := context.WithCancel(context.Background())
	wc := newWSConn(ws, connCancel)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		// Best-effort close on a connection rejected during shutdown.
		_ = ws.Close(websocket.StatusGoingAway, "server shutting down")
		connCancel()
		return
	}
	s.conns[wc] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.conns, wc)
		s.mu.Unlock()
		s.wg.Done()
	}()

	wc.startWriter(connCtx)
	pingDone := make(chan struct{})
	pingCtx, pingCancel := context.WithCancel(connCtx)
	go func() {
		defer close(pingDone)
		s.pingLoop(pingCtx, wc)
	}()

	sess := s.hub.newSession(memberID, wc)
	for {
		_, data, err := ws.Read(connCtx)
		if err != nil {
			break
		}
		sess.handleMessage(connCtx, data)
	}

	pingCancel()
	<-pingDone
	connCancel()
	wc.stopWriter()

	s.mu.Lock()
	shuttingDown := s.closed
	s.mu.Unlock()
	if !shuttingDown {
		sess.disconnect(connCtx)
	}
}

func (s *Server) pingLoop(ctx context.Context, wc *wsConn) {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, s.pingInterval)
			err := wc.ws.Ping(pingCtx)
			cancel()
			if err != nil {
				wc.closeNow()
				return
			}
		}
	}
}

// Shutdown closes all active WebSocket connections with 1001 Going Away,
// cancels grace timers, and waits for connection goroutines to exit until ctx
// expires. If ctx expires first, remaining connections are closed immediately.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]*wsConn, 0, len(s.conns))
	for wc := range s.conns {
		conns = append(conns, wc)
	}
	s.mu.Unlock()

	// Send 1001 Going Away before cancelling connCtx so the close frame can be
	// written on the socket.
	for _, wc := range conns {
		wc.close(closeGoingAway, "server shutting down")
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	var err error
	select {
	case <-done:
	case <-ctx.Done():
		for _, wc := range conns {
			wc.closeNow()
		}
		<-done
		err = ctx.Err()
	}

	// Clean up rooms and voice connections only after every connection read loop
	// has stopped so no new room can be created during shutdown.
	s.hub.shutdown(context.WithoutCancel(ctx))
	return err
}

// wsConn wraps a coder/websocket Conn with a bounded send queue and writer
// goroutine so callers on room goroutines never block and message order is
// preserved per connection.
type wsConn struct {
	ws     *websocket.Conn
	cancel context.CancelFunc

	sendCh       chan []byte
	writeStop    chan struct{}
	writeDone    chan struct{}
	stopOnce     sync.Once
	closeOnce    sync.Once
	closeNowOnce sync.Once
}

func newWSConn(ws *websocket.Conn, cancel context.CancelFunc) *wsConn {
	return &wsConn{
		ws:        ws,
		cancel:    cancel,
		sendCh:    make(chan []byte, sendQueueSize),
		writeStop: make(chan struct{}),
		writeDone: make(chan struct{}),
	}
}

func (c *wsConn) startWriter(ctx context.Context) {
	go func() {
		defer close(c.writeDone)
		for {
			select {
			case <-c.writeStop:
				return
			case <-ctx.Done():
				return
			case msg := <-c.sendCh:
				if err := c.ws.Write(ctx, websocket.MessageText, msg); err != nil {
					c.closeNow()
					return
				}
			}
		}
	}()
}

func (c *wsConn) stopWriter() {
	c.stopOnce.Do(func() {
		close(c.writeStop)
	})
	<-c.writeDone
}

// send enqueues msg for the writer goroutine. If the queue is full, the
// connection is treated as slow and closed immediately with CloseNow.
func (c *wsConn) send(msg []byte) {
	select {
	case c.sendCh <- msg:
	default:
		c.closeNow()
	}
}

// close performs an asynchronous WebSocket close handshake with code and reason.
func (c *wsConn) close(code int, reason string) {
	c.closeOnce.Do(func() {
		go func() {
			// The connection is already being torn down; read loop cleanup handles
			// disconnect regardless of whether the close handshake succeeds.
			_ = c.ws.Close(websocket.StatusCode(code), reason)
		}()
	})
}

// closeNow immediately closes the underlying socket without a close handshake.
func (c *wsConn) closeNow() {
	c.closeNowOnce.Do(func() {
		// Best-effort immediate close; the underlying connection may already be closed.
		_ = c.ws.CloseNow()
		c.cancel()
	})
}
