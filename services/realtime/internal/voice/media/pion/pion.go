// Package pion implements [media.Driver] with Pion WebRTC.
package pion

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/depromeet/19th-team5-server/services/realtime/internal/voice/media"
	"github.com/pion/ice/v4"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

var opus = webrtc.RTPCodecCapability{
	MimeType:    webrtc.MimeTypeOpus,
	ClockRate:   48000,
	Channels:    2,
	SDPFmtpLine: "useinbandfec=1;usedtx=1",
}

// Driver opens connections that all carry voice on one UDP port.
type Driver struct {
	api *webrtc.API
	mux ice.UDPMux
}

// NewDriver returns a Driver that carries voice on packetConn. The Driver takes
// ownership of packetConn, and the caller must call [Driver.Close] to release
// it.
func NewDriver(packetConn net.PacketConn) *Driver {
	return newDriver(packetConn, webrtc.SettingEngine{})
}

// newDriver is [NewDriver] with the settings in s, which lets tests shorten the
// ICE timeouts.
func newDriver(packetConn net.PacketConn, s webrtc.SettingEngine) *Driver {
	var m webrtc.MediaEngine
	// RegisterCodec fails only for an unknown kind or a payload type that
	// another codec has.
	_ = m.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opus, PayloadType: 111}, webrtc.RTPCodecTypeAudio)
	// Every connection gives the audio level extension the same ID, because
	// Pion numbers extensions in the order they are registered here. So a
	// forwarded packet carries its level under the ID that the subscriber
	// negotiated. RegisterHeaderExtension fails only for a direction other
	// than recvonly or sendonly.
	_ = m.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.AudioLevelURI}, webrtc.RTPCodecTypeAudio)

	mux := webrtc.NewICEUDPMux(nil, packetConn)
	s.SetICEUDPMux(mux)
	// The mux offers only the address that packetConn is bound to, so this
	// matters only when packetConn is bound to a loopback address.
	s.SetIncludeLoopbackCandidate(true)
	// Signaling closes the connections. Otherwise Pion closes one when the
	// client closes its side, and later calls on it fail.
	s.DisableCloseByDTLS(true)

	return &Driver{
		api: webrtc.NewAPI(webrtc.WithMediaEngine(&m), webrtc.WithSettingEngine(s)),
		mux: mux,
	}
}

// Open implements [media.Driver]. The participant's voice goes out in a
// stream whose ID is participantID. Open ignores ctx, so it succeeds even if
// ctx is cancelled.
func (d *Driver) Open(_ context.Context, participantID string, onFailed func()) (media.Conn, error) {
	pc, err := d.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, err
	}
	// AddTransceiverFromKind fails only on a closed connection or with an
	// unsupported direction.
	_, _ = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	// NewTrackLocalStaticRTP fails only through its options.
	track, _ := webrtc.NewTrackLocalStaticRTP(opus, "audio", participantID)
	c := &conn{
		pc:          pc,
		track:       track,
		subscribers: make(map[*conn]*webrtc.RTPSender),
		publishers:  make(map[*conn]struct{}),
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateFailed {
			onFailed()
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			p, _, err := remote.ReadRTP()
			if err != nil {
				return
			}
			if c.muted.Load() {
				continue
			}
			// WriteRTP keeps writing to the other subscribers when one fails,
			// and a packet that one subscriber misses is like a packet lost on
			// the network, so there is nothing to do with the error.
			_ = track.WriteRTP(p)
		}
	})
	return c, nil
}

// Close closes the UDP port. The connections that d opened stop carrying
// voice.
func (d *Driver) Close() error {
	return d.mux.Close()
}

// conn ignores the contexts passed to its methods. CreateOffer waits until ICE
// gathering is done.
type conn struct {
	pc *webrtc.PeerConnection
	// track carries the participant's voice to the subscribers.
	track *webrtc.TrackLocalStaticRTP
	// subscribers maps each subscriber to the sender of track on it.
	subscribers map[*conn]*webrtc.RTPSender
	publishers  map[*conn]struct{}
	// muted is read by the goroutine that forwards the participant's voice.
	muted atomic.Bool
	// restartICE is whether the next offer restarts ICE.
	restartICE bool
}

func (c *conn) Close() error {
	var errs []error
	for s, sender := range c.subscribers {
		errs = append(errs, s.pc.RemoveTrack(sender))
		delete(s.publishers, c)
	}
	for p := range c.publishers {
		delete(p.subscribers, c)
	}
	errs = append(errs, c.pc.Close())
	return errors.Join(errs...)
}

func (c *conn) Subscribe(_ context.Context, publisher media.Conn) error {
	p := publisher.(*conn)
	sender, err := c.pc.AddTrack(p.track)
	if err != nil {
		return err
	}
	p.subscribers[c] = sender
	c.publishers[p] = struct{}{}
	return nil
}

func (c *conn) SetMuted(muted bool) {
	c.muted.Store(muted)
}

func (c *conn) RestartICE() {
	c.restartICE = true
}

func (c *conn) CreateOffer(_ context.Context) (string, error) {
	offer, err := c.pc.CreateOffer(&webrtc.OfferOptions{ICERestart: c.restartICE})
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}
	c.restartICE = false
	gathered := webrtc.GatheringCompletePromise(c.pc)
	if err := c.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
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
