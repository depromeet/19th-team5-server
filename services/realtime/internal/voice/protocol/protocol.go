// Package protocol reads and writes the voice.* signaling messages.
package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

const (
	typeJoin      = "voice.join"
	typeOffer     = "voice.offer"
	typeAnswer    = "voice.answer"
	typeCandidate = "voice.candidate"
	typeLeave     = "voice.leave"
	typeError     = "voice.error"
)

type clientEnvelope struct {
	Type    string         `json:"type"`
	Payload jsontext.Value `json:"payload"`
}

type serverEnvelope struct {
	Type    string        `json:"type"`
	Payload ServerMessage `json:"payload"`
}

// ParseError is the error that [Parse] returns for a malformed message.
type ParseError struct {
	// Problem is the same for every message with the same problem.
	Problem string
	// Metadata holds the parts of the problem that differ between messages.
	Metadata map[string]string
}

func (e *ParseError) Error() string {
	return e.Problem
}

// Parse reads a client message. A missing or null payload counts as {}, and
// unknown fields are ignored. If the message is malformed, the error is a
// [*ParseError].
func Parse(data []byte) (ClientMessage, error) {
	var env clientEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, unmarshalError(err, "", "")
	}

	payload := env.Payload
	switch payload.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		payload = jsontext.Value("{}")
	case jsontext.KindBeginObject:
	default:
		return nil, &ParseError{Problem: "payload is not an object"}
	}

	switch env.Type {
	case typeJoin:
		return Join{}, nil
	case typeAnswer:
		return parseAnswer(payload)
	case typeCandidate:
		return parseCandidate(payload)
	case typeLeave:
		return Leave{}, nil
	default:
		return nil, &ParseError{Problem: "unknown message type"}
	}
}

func parseAnswer(payload jsontext.Value) (ClientMessage, error) {
	var a Answer
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, unmarshalError(err, typeAnswer+" payload: ", "/payload")
	}
	if a.SDP == "" {
		return nil, &ParseError{Problem: typeAnswer + " payload: missing sdp"}
	}
	if a.OfferID < 1 {
		return nil, &ParseError{Problem: typeAnswer + " payload: offerId is less than 1"}
	}
	return a, nil
}

func parseCandidate(payload jsontext.Value) (ClientMessage, error) {
	var c struct {
		Candidate        *string `json:"candidate"`
		SDPMid           *string `json:"sdpMid"`
		SDPMLineIndex    *uint16 `json:"sdpMLineIndex"`
		UsernameFragment *string `json:"usernameFragment"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, unmarshalError(err, typeCandidate+" payload: ", "/payload")
	}
	if c.Candidate == nil {
		return nil, &ParseError{Problem: typeCandidate + " payload: missing candidate"}
	}
	if *c.Candidate != "" && c.SDPMid == nil && c.SDPMLineIndex == nil {
		return nil, &ParseError{Problem: typeCandidate + " payload: sdpMid and sdpMLineIndex are both null"}
	}
	return Candidate{
		Candidate:        *c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}, nil
}

// unmarshalError describes an error from [json.Unmarshal] without the details
// of the Go types. prefix starts the problem, and pointer is where the
// unmarshaled value is in the message.
func unmarshalError(err error, prefix, pointer string) *ParseError {
	var semantic *json.SemanticError
	switch {
	case errors.As(err, &semantic):
		return &ParseError{
			Problem:  prefix + "wrong field type",
			Metadata: map[string]string{"field": pointer + string(semantic.JSONPointer)},
		}
	case errors.Is(err, jsontext.ErrDuplicateName):
		return &ParseError{Problem: prefix + "duplicate field"}
	default:
		return &ParseError{Problem: prefix + "invalid JSON"}
	}
}

// Marshal encodes msg in an envelope with its type and payload.
func Marshal(msg ServerMessage) ([]byte, error) {
	return json.Marshal(serverEnvelope{Type: msg.messageType(), Payload: msg})
}
