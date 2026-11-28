package room

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/go-cmp/cmp"
)

type fakeVerifier struct {
	tokens map[string]string
}

func (v fakeVerifier) Verify(_ context.Context, ticket string) (string, error) {
	id, ok := v.tokens[ticket]
	if !ok {
		return "", errors.New("invalid ticket")
	}
	return id, nil
}

func dialWS(t *testing.T, baseURL, ticket string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws?ticket=" + ticket
	c, resp, err := websocket.Dial(t.Context(), wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("websocket.Dial(%q) failed: %v", wsURL, err)
	}
	t.Cleanup(func() {
		t.Helper()
		_ = c.CloseNow()
	})
	return c
}

func readJSON[T any](ctx context.Context, c *websocket.Conn) (string, T, error) {
	var zero T
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, data, err := c.Read(readCtx)
	if err != nil {
		return "", zero, err
	}
	var env struct {
		Type    string `json:"type"`
		Payload T      `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return "", zero, err
	}
	return env.Type, env.Payload, nil
}

func TestServerTicketAuthAndAcceptError(t *testing.T) {
	// Zero Options tests default logger and ping/grace settings.
	srv := NewServer(fakeVerifier{tokens: map[string]string{"ok": "m1"}}, &fakeStore{members: defaultMembers()}, Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	for _, path := range []string{"/ws", "/ws?ticket=bad"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequestWithContext(%s) failed: %v", path, err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s status = %d, want %d", path, resp.StatusCode, http.StatusUnauthorized)
		}
	}

	// Valid ticket without WebSocket Upgrade headers triggers Accept error path.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/ws?ticket=ok", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext(/ws?ticket=ok) failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /ws?ticket=ok failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("GET /ws?ticket=ok without upgrade status = %d, want %d", resp.StatusCode, http.StatusUpgradeRequired)
	}
}

func TestServerJoinDisconnectReconnectAndShutdown(t *testing.T) {
	srv := NewServer(
		fakeVerifier{tokens: map[string]string{"t1": "m1", "t2": "m2"}},
		&fakeStore{members: defaultMembers()},
		Options{
			Logger:       slog.New(slog.DiscardHandler),
			PingInterval: 50 * time.Millisecond,
			GracePeriod:  5 * time.Second,
		},
	)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		t.Helper()
		_ = srv.Shutdown(context.WithoutCancel(t.Context()))
		ts.Close()
	})

	c1 := dialWS(t, ts.URL, "t1")
	c2 := dialWS(t, ts.URL, "t2")

	ctx := t.Context()
	if err := c1.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
		t.Fatalf("c1 write join failed: %v", err)
	}
	typ, joined1, err := readJSON[joinedPayload](ctx, c1)
	if err != nil {
		t.Fatalf("c1 read joined failed: %v", err)
	}
	if typ != typeJoined || joined1.ParticipantID != "p1" {
		t.Fatalf("c1 got (%q, %+v), want (%q, p1)", typ, joined1, typeJoined)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read participants failed: %v", err)
	}

	if err := c2.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
		t.Fatalf("c2 write join failed: %v", err)
	}
	typ, joined2, err := readJSON[joinedPayload](ctx, c2)
	if err != nil {
		t.Fatalf("c2 read joined failed: %v", err)
	}
	if typ != typeJoined || joined2.ParticipantID != "p2" {
		t.Fatalf("c2 got (%q, %+v), want (%q, p2)", typ, joined2, typeJoined)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c2); err != nil {
		t.Fatalf("c2 read participants failed: %v", err)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read updated participants failed: %v", err)
	}

	// Disconnect c1 and check c2 sees RECONNECTING.
	_ = c1.Close(websocket.StatusNormalClosure, "")
	typ, parts, err := readJSON[participantsPayload](ctx, c2)
	if err != nil {
		t.Fatalf("c2 read RECONNECTING participants failed: %v", err)
	}
	if typ != typeParticipants {
		t.Fatalf("c2 got %q, want %q", typ, typeParticipants)
	}
	wantReconnecting := participantsPayload{
		Participants: []participantPayload{
			{ParticipantID: "p1", Nickname: "Alice", Role: RoleInterviewer, State: StateReconnecting},
			{ParticipantID: "p2", Nickname: "Bob", Role: RoleInterviewee, State: StateConnected},
		},
	}
	if diff := cmp.Diff(wantReconnecting, parts); diff != "" {
		t.Errorf("participants after c1 disconnect diff (-want +got):\n%s", diff)
	}

	// Reconnect m1 with a new WebSocket connection and check same p1 and CONNECTED.
	c1New := dialWS(t, ts.URL, "t1")
	if err := c1New.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
		t.Fatalf("c1New write join failed: %v", err)
	}
	typ, rejoined, err := readJSON[joinedPayload](ctx, c1New)
	if err != nil {
		t.Fatalf("c1New read joined failed: %v", err)
	}
	if typ != typeJoined || rejoined.ParticipantID != "p1" {
		t.Fatalf("c1New got (%q, %+v), want (%q, p1)", typ, rejoined, typeJoined)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c1New); err != nil {
		t.Fatalf("c1New read participants failed: %v", err)
	}
	typ, parts, err = readJSON[participantsPayload](ctx, c2)
	if err != nil {
		t.Fatalf("c2 read reconnected participants failed: %v", err)
	}
	if typ != typeParticipants || parts.Participants[0].State != StateConnected {
		t.Fatalf("c2 after reconnect got (%q, %+v), want p1 CONNECTED", typ, parts)
	}

	// Shutdown sends 1001 Going Away to active connections and cleans up.
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	shutdownErr := make(chan error, 1)
	go func() {
		shutdownErr <- srv.Shutdown(shutdownCtx)
	}()

	// Clients reading the connection receive 1001 Going Away and complete the
	// close handshake.
	for _, c := range []*websocket.Conn{c1New, c2} {
		_, _, err := c.Read(shutdownCtx)
		if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
			t.Errorf("close status after Shutdown = %v, want %v (err=%v)", got, websocket.StatusGoingAway, err)
		}
	}
	if err := <-shutdownErr; err != nil {
		t.Fatalf("Shutdown() failed: %v", err)
	}
}

func TestServerShutdownTimeoutForcesCloseNowAndRejectsNewConn(t *testing.T) {
	srv := NewServer(
		fakeVerifier{tokens: map[string]string{"t1": "m1"}},
		&fakeStore{members: defaultMembers()},
		Options{
			Logger:       slog.New(slog.DiscardHandler),
			PingInterval: time.Hour,
		},
	)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c1 := dialWS(t, ts.URL, "t1")
	ctx := t.Context()
	if err := c1.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
		t.Fatalf("c1 write join failed: %v", err)
	}
	if _, _, err := readJSON[joinedPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read joined failed: %v", err)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read participants failed: %v", err)
	}

	// c1 is not reading, so Shutdown with a short timeout falls back to CloseNow.
	shortCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(shortCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown(shortCtx) error = %v, want %v", err, context.DeadlineExceeded)
	}

	// Second Shutdown is a no-op.
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown() error = %v, want nil", err)
	}

	// Connecting after Shutdown gets closed with 1001 Going Away.
	c2 := dialWS(t, ts.URL, "t1")
	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	_, _, err := c2.Read(readCtx)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Errorf("new conn after Shutdown close status = %v, want %v (err=%v)", got, websocket.StatusGoingAway, err)
	}
}

func TestServerPingTimeoutClosesUnresponsiveConnection(t *testing.T) {
	srv := NewServer(
		fakeVerifier{tokens: map[string]string{"t1": "m1", "t2": "m2"}},
		&fakeStore{members: defaultMembers()},
		Options{
			Logger:       slog.New(slog.DiscardHandler),
			PingInterval: 20 * time.Millisecond,
			GracePeriod:  5 * time.Second,
		},
	)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		t.Helper()
		_ = srv.Shutdown(context.WithoutCancel(t.Context()))
		ts.Close()
	})

	ctx := t.Context()
	c1 := dialWS(t, ts.URL, "t1")
	c2 := dialWS(t, ts.URL, "t2")
	for _, c := range []*websocket.Conn{c1, c2} {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
			t.Fatalf("write join failed: %v", err)
		}
		if _, _, err := readJSON[joinedPayload](ctx, c); err != nil {
			t.Fatalf("read joined failed: %v", err)
		}
		if _, _, err := readJSON[participantsPayload](ctx, c); err != nil {
			t.Fatalf("read participants failed: %v", err)
		}
	}

	// c1 stops reading so it never replies to pings; c2 keeps reading and sees p1 RECONNECTING.
	for {
		typ, parts, err := readJSON[participantsPayload](ctx, c2)
		if err != nil {
			t.Fatalf("c2 read participants failed: %v", err)
		}
		if typ == typeParticipants && len(parts.Participants) == 2 && parts.Participants[0].State == StateReconnecting {
			return
		}
	}
}

func TestWSConnSendOverflowClosesNow(t *testing.T) {
	srv := NewServer(fakeVerifier{tokens: map[string]string{"t1": "m1"}}, &fakeStore{members: defaultMembers()}, Options{
		Logger:       slog.New(slog.DiscardHandler),
		PingInterval: time.Hour,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		t.Helper()
		_ = srv.Shutdown(context.WithoutCancel(t.Context()))
		ts.Close()
	})

	c1 := dialWS(t, ts.URL, "t1")
	ctx := t.Context()
	if err := c1.Write(ctx, websocket.MessageText, []byte(`{"type":"room.join","payload":{"interviewId":"i1"}}`)); err != nil {
		t.Fatalf("c1 write join failed: %v", err)
	}
	if _, _, err := readJSON[joinedPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read joined failed: %v", err)
	}
	if _, _, err := readJSON[participantsPayload](ctx, c1); err != nil {
		t.Fatalf("c1 read participants failed: %v", err)
	}

	// Grab the server-side wsConn and overflow its sendCh without starting a new reader.
	srv.mu.Lock()
	var wc *wsConn
	for c := range srv.conns {
		wc = c
	}
	srv.mu.Unlock()
	if wc == nil {
		t.Fatal("len(srv.conns) = 0, want 1")
	}

	wc.stopWriter()
	for range sendQueueSize + 1 {
		wc.send([]byte(`{"type":"room.error"}`))
	}

	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, _, err := c1.Read(readCtx); err == nil {
		t.Errorf("c1.Read() error = %v, want non-nil error after send queue overflow", err)
	}
}
