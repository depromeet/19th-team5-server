// Package media defines the contract between voice signaling and the media
// implementation that runs the WebRTC connections.
package media

import "context"

// Driver opens one WebRTC connection per participant. Open may be called
// concurrently. The participant ID tells the participant's voice apart from
// others when it is forwarded. If Open fails, it leaves no connection behind.
// Otherwise the caller must call [Conn.Close] when it is done with the Conn.
type Driver interface {
	Open(ctx context.Context, participantID string) (Conn, error)
}

// Conn is a participant's WebRTC connection. It is used by one goroutine at a
// time.
type Conn interface {
	// Close ends the connection and the subscriptions it is part of. The Conn
	// must not be used afterwards.
	Close() error
	// CreateOffer creates an SDP offer and sets it as the local description.
	// The offer has all of the connection's ICE candidates.
	CreateOffer(ctx context.Context) (sdp string, err error)
	// ApplyAnswer sets sdp, an SDP answer, as the remote description.
	ApplyAnswer(ctx context.Context, sdp string) error
	// AddCandidate fails until [Conn.ApplyAnswer] has succeeded once.
	AddCandidate(ctx context.Context, c Candidate) error
	// Subscribe forwards the voice of publisher to this connection from its
	// next negotiation on. publisher must come from the same Driver and be used
	// by the same goroutine as this connection.
	Subscribe(ctx context.Context, publisher Conn) error
}

// Candidate is a W3C RTCIceCandidateInit.
type Candidate struct {
	Candidate        string
	SDPMid           *string
	SDPMLineIndex    *uint16
	UsernameFragment *string
}
