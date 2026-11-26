package pion

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
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

	offer, err := c.CreateOffer(ctx)
	if err != nil {
		t.Fatalf("CreateOffer() failed: %v", err)
	}
	for _, want := range []string{
		"a=fmtp:111 useinbandfec=1;usedtx=1",
		fmt.Sprintf("127.0.0.1 %d typ host", port),
	} {
		if !strings.Contains(offer, want) {
			t.Errorf("CreateOffer() = %q, want it to contain %q", offer, want)
		}
	}

	client := newClient(t)
	connected := make(chan struct{})
	client.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			close(connected)
		}
	})
	candidates := make(chan webrtc.ICECandidateInit, 10)
	client.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			candidates <- c.ToJSON()
		}
	})
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		t.Fatalf("client SetRemoteDescription() failed: %v", err)
	}
	answer, err := client.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("client CreateAnswer() failed: %v", err)
	}
	if err := client.SetLocalDescription(answer); err != nil {
		t.Fatalf("client SetLocalDescription() failed: %v", err)
	}
	if err := c.ApplyAnswer(ctx, answer.SDP); err != nil {
		t.Fatalf("ApplyAnswer() failed: %v", err)
	}
	if err := c.AddCandidate(ctx, media.Candidate(<-candidates)); err != nil {
		t.Fatalf("AddCandidate() failed: %v", err)
	}
	<-connected
}
