package pion

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/google/go-cmp/cmp"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

func newTestDriver(ctx context.Context, t *testing.T) (*Driver, int) {
	t.Helper()
	var lc net.ListenConfig
	udp, err := lc.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenPacket() failed: %v", err)
	}
	d := NewDriver(udp)
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("Driver.Close() failed: %v", err)
		}
	})
	return d, udp.LocalAddr().(*net.UDPAddr).Port
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
	d, port := newTestDriver(ctx, t)
	c, err := d.Open(ctx, "p1")
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
		fmt.Sprintf("127.0.0.1 %d typ host", port),
	} {
		if !strings.Contains(offer, want) {
			t.Errorf("CreateOffer() = %q, want it to contain %q", offer, want)
		}
	}
}

// connect answers the offer of c on client and waits until they are connected.
// It returns the offer.
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
	if err := c.AddCandidate(ctx, media.Candidate(<-candidates)); err != nil {
		return "", fmt.Errorf("AddCandidate: %w", err)
	}
	<-connected
	return offer, nil
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
	c1, c2 media.Conn
	// heard gets a packet that client2 received, unless it already holds one.
	heard <-chan heardPacket
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
	d, _ := newTestDriver(ctx, t)
	c1, err := d.Open(ctx, "p1")
	if err != nil {
		return nil, fmt.Errorf("Open(p1): %w", err)
	}
	c2, err := d.Open(ctx, "p2")
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
	return &call{c1: c1, c2: c2, heard: heard}, nil
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
