package protocol

// ClientMessage is a message sent from a client to the server.
type ClientMessage interface {
	isClientMessage()
}

// ServerMessage is a message sent from the server to a client.
type ServerMessage interface {
	messageType() string
}

// Join asks to join the voice channel of the client's current room.
type Join struct{}

// Offer is the server's SDP offer.
type Offer struct {
	SDP     string `json:"sdp"`
	OfferID int    `json:"offerId"`
}

// Answer answers the offer with the same OfferID.
type Answer struct {
	SDP     string `json:"sdp"`
	OfferID int    `json:"offerId"`
}

// Candidate is a W3C RTCIceCandidateInit that the client sends.
type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex"`
	UsernameFragment *string `json:"usernameFragment"`
}

// Leave asks to leave the voice channel.
type Leave struct{}

// ErrorMessage reports that a request or the voice connection failed.
type ErrorMessage struct {
	Reason  ErrorReason `json:"reason"`
	Message string      `json:"message"`
	// Metadata holds the values that differ between errors with the same
	// reason. Errors with the same reason have the same keys.
	Metadata map[string]string `json:"metadata"`
}

// ErrorReason is why a request or the voice connection failed. New reasons may
// be added, so clients must handle reasons that they do not know.
type ErrorReason string

const (
	// ErrorReasonInvalidMessage means [Parse] rejected the message.
	ErrorReasonInvalidMessage ErrorReason = "INVALID_MESSAGE"
	// ErrorReasonConnectionFailed means the server could not set up or
	// negotiate the voice connection, and the connection no longer exists.
	ErrorReasonConnectionFailed ErrorReason = "CONNECTION_FAILED"
)

// isClientMessage ensures that only client messages can be a ClientMessage.
func (Join) isClientMessage()      {}
func (Answer) isClientMessage()    {}
func (Candidate) isClientMessage() {}
func (Leave) isClientMessage()     {}

// messageType gives the type of a server message and ensures that only server
// messages can be a ServerMessage.
func (Offer) messageType() string        { return typeOffer }
func (ErrorMessage) messageType() string { return typeError }
