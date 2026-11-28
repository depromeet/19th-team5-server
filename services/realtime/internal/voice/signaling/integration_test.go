package signaling_test

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
	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/signaling"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// client is a fake client. It answers every offer on its current connection
// and sends a voice packet every 20ms.
type client struct {
	t    *testing.T
	id   string
	room *signaling.Room

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
	// muted is the last mute state of each participant that the room sent on
	// the current connection.
	muted map[string]bool
	// ufrag is the ICE username fragment of the last offer, and ufragChanges
	// counts the offers whose fragment differed from the one before.
	ufrag        string
	ufragChanges int
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
	c.pc, c.mic, c.micOn, c.stop, c.muted = pc, mic, false, stop, make(map[string]bool)
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
	c.room.Receive(ctx, c.id, data)
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
	switch env.Type {
	case "voice.offer":
		c.handleOffer(ctx, env.Payload)
	case "voice.muted":
		var m protocol.Muted
		if err := json.Unmarshal(env.Payload, &m); err != nil {
			c.t.Errorf("%s: json.Unmarshal(%s) failed: %v", c.id, env.Payload, err)
			return
		}
		c.mu.Lock()
		c.muted[m.ParticipantID] = m.Muted
		c.mu.Unlock()
	default:
		c.t.Errorf("%s: room sent %s, want only voice.offer and voice.muted", c.id, data)
	}
}

func (c *client) handleOffer(ctx context.Context, payload jsontext.Value) {
	var offer protocol.Offer
	if err := json.Unmarshal(payload, &offer); err != nil {
		c.t.Errorf("%s: json.Unmarshal(%s) failed: %v", c.id, payload, err)
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
	// Pion gives every media section the same fragment.
	_, rest, ok := strings.Cut(offer.SDP, "a=ice-ufrag:")
	if !ok {
		c.t.Errorf("%s: offer %d has no ICE username fragment", c.id, offer.OfferID)
		return
	}
	ufrag, _, _ := strings.Cut(rest, "\r\n")
	if c.ufrag != "" && ufrag != c.ufrag {
		c.ufragChanges++
	}
	c.ufrag = ufrag
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

// waitMuted waits until the room has told c that the mute state of id is
// muted, or returns an error after 20 seconds.
func (c *client) waitMuted(id string, muted bool) error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		c.mu.Lock()
		got, ok := c.muted[id]
		c.mu.Unlock()
		if ok && got == muted {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s was not told that %s has muted %t", c.id, id, muted)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitUfragChange waits until an offer to c has changed its ICE username
// fragment, or returns an error after 20 seconds.
func (c *client) waitUfragChange() error {
	deadline := time.Now().Add(20 * time.Second)
	for {
		c.mu.Lock()
		changes := c.ufragChanges
		c.mu.Unlock()
		if changes > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s got no offer with a new ICE username fragment", c.id)
		}
		time.Sleep(10 * time.Millisecond)
	}
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

// startCall joins four clients to a room and waits until all of them hear each
// other, or returns an error after 20 seconds. The clients leave when the test
// ends.
func startCall(ctx context.Context, t *testing.T) (map[string]*client, error) {
	t.Helper()
	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	driver := pion.NewDriver(udp)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Errorf("Driver.Close() failed: %v", err)
		}
	})

	clients := make(map[string]*client)
	r := signaling.NewServer(driver, slog.New(slog.DiscardHandler)).NewRoom(func(ctx context.Context, participantID string, msg []byte) {
		clients[participantID].handle(ctx, msg)
	})
	for _, id := range []string{"p1", "p2", "p3", "p4"} {
		clients[id] = &client{t: t, id: id, room: r, heard: make(map[string]time.Time)}
	}
	// Cleanups run last first, so the clients leave before the driver closes.
	// t.Context() is cancelled before the cleanups run.
	cleanupCtx := context.WithoutCancel(ctx)
	t.Cleanup(func() {
		for _, c := range clients {
			c.leave(cleanupCtx)
		}
	})

	start := time.Now()
	for _, c := range clients {
		c.join(ctx)
	}
	if err := waitHearing(clients, start); err != nil {
		return nil, err
	}
	return clients, nil
}

func TestVoiceCall(t *testing.T) {
	ctx := t.Context()
	clients, err := startCall(ctx, t)
	if err != nil {
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

	start := time.Now()
	clients["p1"].join(ctx)
	if err := waitHearing(clients, start); err != nil {
		t.Fatalf("after p1 sent voice.join again, %v", err)
	}
}

func TestMute(t *testing.T) {
	ctx := t.Context()
	clients, err := startCall(ctx, t)
	if err != nil {
		t.Fatalf("after all clients joined, %v", err)
	}
	p1 := clients["p1"]

	p1.send(ctx, "voice.setMuted", protocol.SetMuted{Muted: true})
	for id, c := range clients {
		if id == "p1" {
			continue
		}
		if err := c.waitMuted("p1", true); err != nil {
			t.Errorf("after p1 muted, %v", err)
		}
	}
	// The room mutes p1 before it sends voice.muted, but packets that it
	// forwarded before may still be on their way.
	time.Sleep(100 * time.Millisecond)
	muted := time.Now()
	time.Sleep(200 * time.Millisecond)
	for id, c := range clients {
		if id != "p1" && c.hearsSince(muted, []string{"p1"}) {
			t.Errorf("%s heard p1 within 200ms while p1 was muted", id)
		}
	}

	p4 := clients["p4"]
	p4.leave(ctx)
	p4.join(ctx)
	if err := p4.waitMuted("p1", true); err != nil {
		t.Errorf("after p4 rejoined, %v", err)
	}

	unmuted := time.Now()
	p1.send(ctx, "voice.setMuted", protocol.SetMuted{Muted: false})
	if err := waitHearing(clients, unmuted); err != nil {
		t.Fatalf("after p1 unmuted, %v", err)
	}
}

func TestRestartICE(t *testing.T) {
	ctx := t.Context()
	clients, err := startCall(ctx, t)
	if err != nil {
		t.Fatalf("after all clients joined, %v", err)
	}
	p1 := clients["p1"]

	p1.send(ctx, "voice.restartIce", nil)
	if err := p1.waitUfragChange(); err != nil {
		t.Fatalf("after p1 sent voice.restartIce, %v", err)
	}
	restarted := time.Now()
	if err := waitHearing(clients, restarted); err != nil {
		t.Errorf("after the ICE restart of p1, %v", err)
	}
}
