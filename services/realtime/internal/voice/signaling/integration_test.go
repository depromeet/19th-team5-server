package signaling

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media/pion"
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/protocol"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// client is a fake client. It answers every offer on its current connection
// and sends a voice packet every 20ms.
type client struct {
	t    *testing.T
	id   string
	room *room

	mu sync.Mutex
	pc *webrtc.PeerConnection
	// mic is sent on pc once an offer has a section for it.
	mic   *webrtc.TrackLocalStaticRTP
	micOn bool
	// stop ends the packets of mic.
	stop chan struct{}
	// heard is when a packet last arrived from each stream ID.
	heard map[string]time.Time
	// maxMedia is the most media sections that an offer had.
	maxMedia int
}

func (c *client) join(ctx context.Context) {
	c.t.Helper()
	c.closeConnection()
	var s webrtc.SettingEngine
	s.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(s)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		c.t.Fatalf("NewPeerConnection() failed: %v", err)
	}
	mic, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "mic", c.id)
	if err != nil {
		c.t.Fatalf("NewTrackLocalStaticRTP() failed: %v", err)
	}
	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			c.send(ctx, "voice.candidate", protocol.Candidate(cand.ToJSON()))
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
	go func() {
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
				if err := mic.WriteRTP(p); err != nil {
					c.t.Errorf("%s: WriteRTP() failed: %v", c.id, err)
				}
			}
		}
	}()

	c.mu.Lock()
	c.pc, c.mic, c.micOn, c.stop = pc, mic, false, stop
	c.mu.Unlock()
	c.send(ctx, "voice.join", nil)
}

func (c *client) leave(ctx context.Context) {
	c.send(ctx, "voice.leave", nil)
	c.closeConnection()
}

func (c *client) closeConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pc == nil {
		return
	}
	close(c.stop)
	if err := c.pc.Close(); err != nil {
		c.t.Errorf("%s: Close() failed: %v", c.id, err)
	}
	c.pc = nil
}

func (c *client) send(ctx context.Context, typ string, payload any) {
	data, err := json.Marshal(struct {
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{typ, payload})
	if err != nil {
		c.t.Errorf("%s: json.Marshal() failed: %v", c.id, err)
		return
	}
	c.room.receive(ctx, c.id, data)
}

// handle handles a message from the room on the room goroutine.
func (c *client) handle(ctx context.Context, data []byte) {
	var env struct {
		Type    string         `json:"type"`
		Payload jsontext.Value `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		c.t.Errorf("%s: json.Unmarshal(%s) failed: %v", c.id, data, err)
		return
	}
	if env.Type != "voice.offer" {
		c.t.Errorf("%s: room sent %s, want only voice.offer", c.id, data)
		return
	}
	var offer protocol.Offer
	if err := json.Unmarshal(env.Payload, &offer); err != nil {
		c.t.Errorf("%s: json.Unmarshal(%s) failed: %v", c.id, env.Payload, err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	pc := c.pc
	if pc == nil {
		// Offers can still arrive after the client has left.
		return
	}
	c.maxMedia = max(c.maxMedia, strings.Count(offer.SDP, "\nm="))
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		c.t.Errorf("%s: SetRemoteDescription(offer %d) failed: %v", c.id, offer.OfferID, err)
		return
	}
	if !c.micOn {
		// Like a browser, the client sends its mic on the first section that it
		// may send on.
		if _, err := pc.AddTrack(c.mic); err != nil {
			c.t.Errorf("%s: AddTrack() failed: %v", c.id, err)
			return
		}
		c.micOn = true
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		c.t.Errorf("%s: CreateAnswer() failed: %v", c.id, err)
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		c.t.Errorf("%s: SetLocalDescription() failed: %v", c.id, err)
		return
	}
	c.send(ctx, "voice.answer", protocol.Answer{SDP: answer.SDP, OfferID: offer.OfferID})
}

// hearsSince reports whether c has heard each of ids since since.
func (c *client) hearsSince(since time.Time, ids []string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		if !c.heard[id].After(since) {
			return false
		}
	}
	return true
}

// waitHearing waits until every client has heard all the others since the
// given time, or returns an error after 20 seconds.
func waitHearing(clients map[string]*client, since time.Time) error {
	deadline := time.Now().Add(20 * time.Second)
	for id, c := range clients {
		var others []string
		for other := range clients {
			if other != id {
				others = append(others, other)
			}
		}
		for !c.hearsSince(since, others) {
			if time.Now().After(deadline) {
				return fmt.Errorf("%s did not hear all of %v", id, others)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

func TestVoiceCall(t *testing.T) {
	ctx := t.Context()
	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	driver := pion.NewDriver(udp)
	defer func() {
		if err := driver.Close(); err != nil {
			t.Errorf("Driver.Close() failed: %v", err)
		}
	}()

	clients := make(map[string]*client)
	r := newRoom(driver, func(ctx context.Context, participantID string, msg []byte) {
		clients[participantID].handle(ctx, msg)
	}, slog.New(slog.DiscardHandler))
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		clients[id] = &client{t: t, id: id, room: r, heard: make(map[string]time.Time)}
	}
	defer func() {
		for _, c := range clients {
			c.leave(ctx)
		}
	}()

	start := time.Now()
	for _, c := range clients {
		c.join(ctx)
	}
	if err := waitHearing(clients, start); err != nil {
		t.Fatalf("after all clients joined, %v", err)
	}
	wantMedia := clients["p1"].maxMedia

	p4 := clients["p4"]
	for i := range 100 {
		p4.leave(ctx)
		rejoined := time.Now()
		p4.join(ctx)
		if err := waitHearing(clients, rejoined); err != nil {
			t.Fatalf("after p4 rejoined %d times, %v", i+1, err)
		}
	}
	// A rejoin before the removal of p4's track is negotiated adds a section,
	// which later rejoins reuse.
	for id, c := range clients {
		if c.maxMedia > wantMedia+1 {
			t.Errorf("after 100 rejoins, %s got an offer with %d media sections, want at most %d", id, c.maxMedia, wantMedia+1)
		}
	}

	start = time.Now()
	clients["p1"].join(ctx)
	if err := waitHearing(clients, start); err != nil {
		t.Fatalf("after p1 sent voice.join again, %v", err)
	}
}
