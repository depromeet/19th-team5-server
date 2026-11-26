// Package pion implements [media.Driver] with Pion WebRTC.
package pion

import (
	"context"
	"net"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// Driver opens connections that all carry voice on one UDP port.
type Driver struct {
	api *webrtc.API
	mux ice.UDPMux
}

// NewDriver returns a Driver that carries voice on conn. The Driver takes
// ownership of conn, and the caller must call [Driver.Close] to release it.
func NewDriver(conn net.PacketConn) *Driver {
	var m webrtc.MediaEngine
	// RegisterCodec fails only for an unknown kind or a payload type that
	// another codec has.
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000,
		Channels:    2,
		SDPFmtpLine: "useinbandfec=1;usedtx=1",
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio)

	mux := webrtc.NewICEUDPMux(nil, conn)
	var s webrtc.SettingEngine
	s.SetICEUDPMux(mux)
	// The mux offers only the address that conn is bound to, so this matters
	// only when conn is bound to a loopback address.
	s.SetIncludeLoopbackCandidate(true)

	return &Driver{
		api: webrtc.NewAPI(webrtc.WithMediaEngine(&m), webrtc.WithSettingEngine(s)),
		mux: mux,
	}
}

// Open implements [media.Driver].
func (d *Driver) Open(_ context.Context, _ string) (media.Conn, error) {
	pc, err := d.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	// AddTransceiverFromKind fails only on a closed connection or with an
	// unsupported direction.
	_, _ = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	return &conn{pc: pc}, nil
}

// Close closes the UDP port. The connections that d opened stop carrying
// voice.
func (d *Driver) Close() error {
	return d.mux.Close()
}

type conn struct {
	pc *webrtc.PeerConnection
}

func (c *conn) Close() error {
	return c.pc.Close()
}

func (c *conn) CreateOffer(_ context.Context) (string, error) {
	offer, err := c.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	gathered := webrtc.GatheringCompletePromise(c.pc)
	if err := c.pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	<-gathered
	return c.pc.LocalDescription().SDP, nil
}

func (c *conn) ApplyAnswer(_ context.Context, sdp string) error {
	return c.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

func (c *conn) AddCandidate(_ context.Context, cand media.Candidate) error {
	return c.pc.AddICECandidate(webrtc.ICECandidateInit(cand))
}
