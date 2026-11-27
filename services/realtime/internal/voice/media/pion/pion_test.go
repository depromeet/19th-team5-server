package pion

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/google/go-cmp/cmp"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// blockableConn drops every packet that it reads or writes while blocked is
// true.
type blockableConn struct {
	net.PacketConn
	blocked atomic.Bool
}

func (c *blockableConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil || !c.blocked.Load() {
			return n, addr, err
		}
	}
}

func (c *blockableConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if c.blocked.Load() {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

// newTestDriver returns a Driver on udp, whose connections fail one second
// after udp is blocked.
func newTestDriver(ctx context.Context, t *testing.T) (d *Driver, udp *blockableConn) {
	t.Helper()
	var lc net.ListenConfig
	packetConn, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	udp = &blockableConn{PacketConn: packetConn}
	var s webrtc.SettingEngine
	// The server checks the path every 100ms, so a connection that is not
	// blocked never goes a second without hearing from its client.
	s.SetICETimeouts(500*time.Millisecond, 500*time.Millisecond, 100*time.Millisecond)
	d = newDriver(udp, s)
	closeOnCleanup(t, d)
	return d, udp
}

func closeOnCleanup(t *testing.T, d *Driver) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		if err := d.Close(); err != nil {
			t.Errorf("Driver.Close() failed: %v", err)
		}
	})
}

// newClient returns a client that, like a browser, supports the audio level
// extension.
func newClient(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	var m webrtc.MediaEngine
	// RegisterDefaultCodecs fails only for a payload type that another codec
	// has, and m has no codecs yet.
	_ = m.RegisterDefaultCodecs()
	// RegisterHeaderExtension fails only for a direction other than recvonly
	// or sendonly.
	_ = m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.AudioLevelURI}, webrtc.RTPCodecTypeAudio)
	var s webrtc.SettingEngine
	s.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(&m), webrtc.WithSettingEngine(s)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("NewPeerConnection() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Errorf("client Close() failed: %v", err)
		}
	})
	return pc
}

func TestConnect(t *testing.T) {
	ctx := t.Context()
	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	d := NewDriver(udp)
	closeOnCleanup(t, d)
	c, err := d.Open(ctx, "p1", func() {})
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()

	offer, err := connect(ctx, c, newClient(t))
	if err != nil {
		t.Fatalf("connect() failed: %v", err)
	}

	for _, want := range []string{
		"a=fmtp:111 useinbandfec=1;usedtx=1",
		fmt.Sprintf("127.0.0.1 %d typ host", udp.LocalAddr().(*net.UDPAddr).Port),
	} {
		if !strings.Contains(offer, want) {
			t.Errorf("CreateOffer() = %q, want it to contain %q", offer, want)
		}
	}
}

// connect answers the offer of c on client and waits until they are connected,
// or returns an error after 10 seconds. It returns the offer.
func connect(ctx context.Context, c media.Conn, client *webrtc.PeerConnection) (string, error) {
	connected := make(chan struct{})
	client.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			close(connected)
		}
	})
	candidates := make(chan webrtc.ICECandidateInit, 10)
	client.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand != nil {
			candidates <- cand.ToJSON()
		}
	})
	offer, err := c.CreateOffer(ctx)
	if err != nil {
		return "", fmt.Errorf("CreateOffer: %w", err)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("client SetRemoteDescription: %w", err)
	}
	answer, err := client.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("client CreateAnswer: %w", err)
	}
	if err := client.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("client SetLocalDescription: %w", err)
	}
	if err := c.ApplyAnswer(ctx, answer.SDP); err != nil {
		return "", fmt.Errorf("ApplyAnswer: %w", err)
	}
	timeout := time.After(10 * time.Second)
	select {
	case cand := <-candidates:
		if err := c.AddCandidate(ctx, media.Candidate(cand)); err != nil {
			return "", fmt.Errorf("AddCandidate: %w", err)
		}
	case <-timeout:
		return "", errors.New("client gathered no candidate within 10s")
	}
	select {
	case <-connected:
		return offer, nil
	case <-timeout:
		return "", errors.New("client did not connect within 10s")
	}
}

// micLevel is the audio level of the packets that client1 sends.
var micLevel = rtp.AudioLevelExtension{Level: 30, Voice: true}

// heardPacket is what client2 read from a packet.
type heardPacket struct {
	StreamID string
	// Level is zero if the packet had no audio level.
	Level rtp.AudioLevelExtension
}

// call has p1 and p2 subscribed to each other and connected to two clients.
type call struct {
	c1, c2           media.Conn
	client1, client2 *webrtc.PeerConnection
	// udp carries the voice of c1 and c2.
	udp *blockableConn
	// heard gets a packet that client2 received, unless it already holds one.
	heard <-chan heardPacket
	// failed gets the participant ID of c1 or c2 each time it fails, unless it
	// already holds two IDs.
	failed <-chan string
}

// audioLevelID returns the ID that exts give the audio level extension. ok is
// false if exts do not have it.
func audioLevelID(exts []webrtc.RTPHeaderExtensionParameter) (id uint8, ok bool) {
	for _, e := range exts {
		if e.URI == sdp.AudioLevelURI {
			return uint8(e.ID), true //nolint:gosec // Pion gives IDs from 1 to 14.
		}
	}
	return 0, false
}

// startCall starts a call in which client1 sends a voice packet with micLevel
// every 20ms until the test ends. Unless it returns an error, the test must
// close c1 and c2.
func startCall(ctx context.Context, t *testing.T) (*call, error) {
	t.Helper()
	d, udp := newTestDriver(ctx, t)
	failed := make(chan string, 2)
	onFailed := func(participantID string) func() {
		return func() {
			select {
			case failed <- participantID:
			default:
			}
		}
	}
	c1, err := d.Open(ctx, "p1", onFailed("p1"))
	if err != nil {
		return nil, fmt.Errorf("Open(p1): %w", err)
	}
	c2, err := d.Open(ctx, "p2", onFailed("p2"))
	if err != nil {
		return nil, fmt.Errorf("Open(p2): %w", err)
	}
	// Each subscribes to the other, so that closing c1 ends a subscription in
	// each direction.
	if err := c1.Subscribe(ctx, c2); err != nil {
		return nil, fmt.Errorf("p1 Subscribe(p2): %w", err)
	}
	if err := c2.Subscribe(ctx, c1); err != nil {
		return nil, fmt.Errorf("p2 Subscribe(p1): %w", err)
	}

	client1, client2 := newClient(t), newClient(t)
	mic, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "mic", "client1")
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP() failed: %v", err)
	}
	sender, err := client1.AddTrack(mic)
	if err != nil {
		t.Fatalf("client1 AddTrack() failed: %v", err)
	}
	heard := make(chan heardPacket, 1)
	client2.OnTrack(func(remote *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		// Without the extension, Level stays zero and TestSubscribe fails.
		recvID, _ := audioLevelID(receiver.GetParameters().HeaderExtensions)
		for {
			p, _, err := remote.ReadRTP()
			if err != nil {
				return
			}
			h := heardPacket{StreamID: remote.StreamID()}
			// Unmarshal leaves Level zero if the packet has no audio level.
			_ = h.Level.Unmarshal(p.GetExtension(recvID))
			select {
			case heard <- h:
			default:
			}
		}
	})
	if _, err := connect(ctx, c1, client1); err != nil {
		return nil, fmt.Errorf("connect(p1): %w", err)
	}
	if _, err := connect(ctx, c2, client2); err != nil {
		return nil, fmt.Errorf("connect(p2): %w", err)
	}
	sendID, ok := audioLevelID(sender.GetParameters().HeaderExtensions)
	if !ok {
		return nil, errors.New("client1 did not negotiate the audio level extension with p1")
	}
	// Marshal fails only for a level above 127.
	rawLevel, _ := micLevel.Marshal()

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		p := &rtp.Packet{Version: 2, Payload: []byte{0xf8, 0xff, 0xfe}}
		// SetExtension fails only on a packet that already has extensions.
		_ = p.SetExtension(sendID, rawLevel)
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				p.SequenceNumber++
				p.Timestamp += 960
				if err := mic.WriteRTP(p); err != nil {
					t.Errorf("client1 WriteRTP() failed: %v", err)
					return
				}
			}
		}
	}()
	// Cleanups run last first, so the packets stop before the clients close.
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	return &call{c1: c1, c2: c2, client1: client1, client2: client2, udp: udp, heard: heard, failed: failed}, nil
}

// waitHeard returns a packet that client2 receives, or an error after 10
// seconds.
func (c *call) waitHeard() (heardPacket, error) {
	select {
	case h := <-c.heard:
		return h, nil
	case <-time.After(10 * time.Second):
		return heardPacket{}, errors.New("client2 heard nothing within 10s")
	}
}

func TestSubscribe(t *testing.T) {
	c, err := startCall(t.Context(), t)
	if err != nil {
		t.Fatalf("startCall() failed: %v", err)
	}

	got, err := c.waitHeard()
	if err != nil {
		t.Fatal(err)
	}
	// The audio level shows that p2 gets the extension under the ID that p1
	// got it.
	want := heardPacket{StreamID: "p1", Level: micLevel}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("after p2 Subscribe(p1), client2 heard a packet that differs from what client1 sent (-want +got):\n%s", diff)
	}

	if err := c.c1.Close(); err != nil {
		t.Errorf("p1 Close() failed: %v", err)
	}
	// c2 fails to close if it still holds the closed c1.
	if err := c.c2.Close(); err != nil {
		t.Errorf("p2 Close() after p1 closed failed: %v", err)
	}
}

func TestSetMuted(t *testing.T) {
	c, err := startCall(t.Context(), t)
	if err != nil {
		t.Fatalf("startCall() failed: %v", err)
	}
	defer func() {
		if err := errors.Join(c.c1.Close(), c.c2.Close()); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()
	if _, err := c.waitHeard(); err != nil {
		t.Fatal(err)
	}

	c.c1.SetMuted(true)
	// Packets that p1 forwarded before SetMuted may still be on their way.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-c.heard:
	default:
	}
	select {
	case <-c.heard:
		t.Error("client2 heard p1 within 200ms while p1 was muted")
	case <-time.After(200 * time.Millisecond):
	}

	c.c1.SetMuted(false)
	if _, err := c.waitHeard(); err != nil {
		t.Errorf("after SetMuted(false), %v", err)
	}
}

func TestRestartICE(t *testing.T) {
	ctx := t.Context()
	c, err := startCall(ctx, t)
	if err != nil {
		t.Fatalf("startCall() failed: %v", err)
	}
	defer func() {
		if err := errors.Join(c.c1.Close(), c.c2.Close()); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()
	if _, err := c.waitHeard(); err != nil {
		t.Fatal(err)
	}

	c.udp.blocked.Store(true)
	got := make(map[string]bool)
	timeout := time.After(10 * time.Second)
	for len(got) < 2 {
		select {
		case id := <-c.failed:
			got[id] = true
		case <-timeout:
			t.Fatalf("after the UDP port was blocked, only %v failed within 10s, want p1 and p2", got)
		}
	}
	// Nothing arrives while the port is blocked, so a packet heard after this
	// came over the new path.
	select {
	case <-c.heard:
	default:
	}
	c.udp.blocked.Store(false)

	// A failed connection stays failed without an ICE restart.
	c.c1.RestartICE()
	c.c2.RestartICE()
	if _, err := connect(ctx, c.c1, c.client1); err != nil {
		t.Fatalf("connect(p1) after RestartICE() failed: %v", err)
	}
	if _, err := connect(ctx, c.c2, c.client2); err != nil {
		t.Fatalf("connect(p2) after RestartICE() failed: %v", err)
	}
	if _, err := c.waitHeard(); err != nil {
		t.Errorf("after RestartICE(), %v", err)
	}
}
