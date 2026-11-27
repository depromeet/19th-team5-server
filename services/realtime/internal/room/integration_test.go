package room_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/room"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media/pion"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/signaling"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type mapVerifier map[string]string

func (v mapVerifier) Verify(_ context.Context, ticket string) (string, error) {
	id, ok := v[ticket]
	if !ok {
		return "", errors.New("invalid ticket")
	}
	return id, nil
}

type staticStore []room.Member

func (s staticStore) Participants(_ context.Context, _ string) ([]room.Member, error) {
	return s, nil
}

type wsVoiceClient struct {
	t      *testing.T
	ticket string
	wsURL  string

	mu    sync.Mutex
	ws    *websocket.Conn
	pid   string
	pc    *webrtc.PeerConnection
	mic   *webrtc.TrackLocalStaticRTP
	micOn bool
	stop  chan struct{}
	heard map[string]time.Time
	wg    sync.WaitGroup
}

func newWSVoiceClient(t *testing.T, baseURL, ticket string) *wsVoiceClient {
	return &wsVoiceClient{
		t:      t,
		ticket: ticket,
		wsURL:  "ws" + strings.TrimPrefix(baseURL, "http") + "/ws?ticket=" + ticket,
		heard:  make(map[string]time.Time),
	}
}

func (c *wsVoiceClient) connectAndJoinRoom(ctx context.Context, interviewID string) (string, error) {
	ws, resp, err := websocket.Dial(ctx, c.wsURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()

	if err := c.sendJSON(ctx, "room.join", map[string]string{"interviewId": interviewID}); err != nil {
		return "", err
	}
	joinedCh := make(chan string, 1)
	c.wg.Go(func() {
		c.readLoop(ctx, ws, joinedCh)
	})

	select {
	case pid := <-joinedCh:
		c.mu.Lock()
		c.pid = pid
		c.mu.Unlock()
		return pid, nil
	case <-time.After(5 * time.Second):
		return "", errors.New("timed out waiting for room.joined")
	}
}

func (c *wsVoiceClient) joinVoice(ctx context.Context) error {
	var s webrtc.SettingEngine
	s.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(s)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	mic, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "mic", c.ticket)
	if err != nil {
		_ = pc.Close()
		return err
	}
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			_ = c.sendJSON(ctx, "voice.candidate", protocol.Candidate(cand.ToJSON()))
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := remote.ReadRTP(); err != nil {
				return
			}
			c.mu.Lock()
			c.heard[remote.StreamID()] = time.Now()
			c.mu.Unlock()
		}
	})

	stop := make(chan struct{})
	c.wg.Go(func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		p := &rtp.Packet{Version: 2, Payload: []byte{0xf8, 0xff, 0xfe}}
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				p.SequenceNumber++
				p.Timestamp += 960
				_ = mic.WriteRTP(p)
			}
		}
	})

	c.mu.Lock()
	c.pc, c.mic, c.micOn, c.stop = pc, mic, false, stop
	c.mu.Unlock()
	return c.sendJSON(ctx, "voice.join", nil)
}

func (c *wsVoiceClient) readLoop(ctx context.Context, ws *websocket.Conn, joinedCh chan<- string) {
	sentJoined := false
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var env struct {
			Type    string         `json:"type"`
			Payload jsontext.Value `json:"payload"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		switch env.Type {
		case "room.joined":
			var p struct {
				ParticipantID string `json:"participantId"`
			}
			if err := json.Unmarshal(env.Payload, &p); err == nil && !sentJoined {
				sentJoined = true
				joinedCh <- p.ParticipantID
			}
		case "voice.offer":
			c.handleOffer(ctx, env.Payload)
		}
	}
}

func (c *wsVoiceClient) handleOffer(ctx context.Context, payload jsontext.Value) {
	var offer protocol.Offer
	if err := json.Unmarshal(payload, &offer); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pc == nil {
		return
	}
	if err := c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		c.t.Errorf("%s: SetRemoteDescription failed: %v", c.ticket, err)
		return
	}
	if !c.micOn {
		if _, err := c.pc.AddTrack(c.mic); err != nil {
			c.t.Errorf("%s: AddTrack failed: %v", c.ticket, err)
			return
		}
		c.micOn = true
	}
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		c.t.Errorf("%s: CreateAnswer failed: %v", c.ticket, err)
		return
	}
	if err := c.pc.SetLocalDescription(answer); err != nil {
		c.t.Errorf("%s: SetLocalDescription failed: %v", c.ticket, err)
		return
	}
	_ = c.sendJSONLocked(ctx, "voice.answer", protocol.Answer{SDP: answer.SDP, OfferID: offer.OfferID})
}

func (c *wsVoiceClient) sendJSON(ctx context.Context, typ string, payload any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendJSONLocked(ctx, typ, payload)
}

func (c *wsVoiceClient) sendJSONLocked(ctx context.Context, typ string, payload any) error {
	if c.ws == nil {
		return errors.New("websocket not connected")
	}
	data, err := json.Marshal(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{typ, payload})
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, data)
}

func (c *wsVoiceClient) waitHearing(streamID string, since time.Time) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		c.mu.Lock()
		heardAt := c.heard[streamID]
		c.mu.Unlock()
		if heardAt.After(since) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not hear %s", c.ticket, streamID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *wsVoiceClient) close() {
	c.mu.Lock()
	if c.stop != nil {
		close(c.stop)
		c.stop = nil
	}
	if c.pc != nil {
		_ = c.pc.Close()
		c.pc = nil
	}
	if c.ws != nil {
		_ = c.ws.CloseNow()
		c.ws = nil
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func TestRoomAndVoiceOverWebSocket(t *testing.T) {
	ctx := t.Context()

	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	driver := pion.NewDriver(udp)
	t.Cleanup(func() {
		t.Helper()
		if err := driver.Close(); err != nil {
			t.Errorf("Driver.Close() failed: %v", err)
		}
	})

	voiceSrv := signaling.NewServer(driver, slog.New(slog.DiscardHandler))
	roomSrv := room.NewServer(
		mapVerifier{"t1": "m1", "t2": "m2"},
		staticStore{
			{MemberID: "m1", Nickname: "Alice", Role: room.RoleInterviewer},
			{MemberID: "m2", Nickname: "Bob", Role: room.RoleInterviewee},
		},
		room.Options{
			Logger: slog.New(slog.DiscardHandler),
			NewVoiceRoom: func(send func(ctx context.Context, participantID string, msg []byte)) room.VoiceRoom {
				return voiceSrv.NewRoom(send)
			},
		},
	)
	ts := httptest.NewServer(roomSrv)
	t.Cleanup(func() {
		t.Helper()
		_ = roomSrv.Shutdown(context.WithoutCancel(ctx))
		ts.Close()
	})

	c1 := newWSVoiceClient(t, ts.URL, "t1")
	c2 := newWSVoiceClient(t, ts.URL, "t2")
	t.Cleanup(func() {
		t.Helper()
		c1.close()
		c2.close()
	})

	pid1, err := c1.connectAndJoinRoom(ctx, "interview-1")
	if err != nil {
		t.Fatalf("c1 join room failed: %v", err)
	}
	pid2, err := c2.connectAndJoinRoom(ctx, "interview-1")
	if err != nil {
		t.Fatalf("c2 join room failed: %v", err)
	}

	start := time.Now()
	if err := c1.joinVoice(ctx); err != nil {
		t.Fatalf("c1 join voice failed: %v", err)
	}
	if err := c2.joinVoice(ctx); err != nil {
		t.Fatalf("c2 join voice failed: %v", err)
	}

	if err := c1.waitHearing(pid2, start); err != nil {
		t.Fatalf("c1 waitHearing(%s): %v", pid2, err)
	}
	if err := c2.waitHearing(pid1, start); err != nil {
		t.Fatalf("c2 waitHearing(%s): %v", pid1, err)
	}
}
