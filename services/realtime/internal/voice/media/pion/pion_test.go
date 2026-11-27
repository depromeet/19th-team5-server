package pion

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/pion/rtp"
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

func newClient(t *testing.T) *webrtc.PeerConnection {
	t.Helper()
	var s webrtc.SettingEngine
	s.SetIncludeLoopbackCandidate(true)
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(s)).NewPeerConnection(webrtc.Configuration{})
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

func TestSubscribe(t *testing.T) {
	ctx := t.Context()
	d, _ := newTestDriver(ctx, t)
	c1, err := d.Open(ctx, "p1")
	if err != nil {
		t.Fatalf("Open(p1) failed: %v", err)
	}
	c2, err := d.Open(ctx, "p2")
	if err != nil {
		t.Fatalf("Open(p2) failed: %v", err)
	}
	// Each subscribes to the other, so that closing c1 ends a subscription in
	// each direction.
	if err := c1.Subscribe(ctx, c2); err != nil {
		t.Fatalf("p1 Subscribe(p2) failed: %v", err)
	}
	if err := c2.Subscribe(ctx, c1); err != nil {
		t.Fatalf("p2 Subscribe(p1) failed: %v", err)
	}

	client1, client2 := newClient(t), newClient(t)
	mic, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "mic", "client1")
	if err != nil {
		t.Fatalf("NewTrackLocalStaticRTP() failed: %v", err)
	}
	if _, err := client1.AddTrack(mic); err != nil {
		t.Fatalf("client1 AddTrack() failed: %v", err)
	}
	heard := make(chan string, 1)
	client2.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if _, _, err := remote.ReadRTP(); err != nil {
			return
		}
		select {
		case heard <- remote.StreamID():
		default:
		}
	})
	if _, err := connect(ctx, c1, client1); err != nil {
		t.Fatalf("connect(p1) failed: %v", err)
	}
	if _, err := connect(ctx, c2, client2); err != nil {
		t.Fatalf("connect(p2) failed: %v", err)
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	p := &rtp.Packet{Version: 2, Payload: []byte{0xf8, 0xff, 0xfe}}
	timeout := time.After(10 * time.Second)
	var got string
	for got == "" {
		select {
		case got = <-heard:
		case <-ticker.C:
			p.SequenceNumber++
			p.Timestamp += 960
			if err := mic.WriteRTP(p); err != nil {
				t.Fatalf("client1 WriteRTP() failed: %v", err)
			}
		case <-timeout:
			t.Fatal("client2 heard nothing from p1 within 10s")
		}
	}
	if got != "p1" {
		t.Errorf("client2 heard stream %q, want p1", got)
	}

	if err := c1.Close(); err != nil {
		t.Errorf("p1 Close() failed: %v", err)
	}
	// c2 fails to close if it still holds the closed c1.
	if err := c2.Close(); err != nil {
		t.Errorf("p2 Close() after p1 closed failed: %v", err)
	}
}
