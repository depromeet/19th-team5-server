// Package protocol reads and writes the voice.* signaling messages.
package protocol

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

const (
	typeJoin       = "voice.join"
	typeOffer      = "voice.offer"
	typeAnswer     = "voice.answer"
	typeCandidate  = "voice.candidate"
	typeRestartICE = "voice.restartIce"
	typeSetMuted   = "voice.setMuted"
	typeMuted      = "voice.muted"
	typeLeave      = "voice.leave"
	typeError      = "voice.error"
)

type clientEnvelope struct {
	Type    string         `json:"type"`
	Payload jsontext.Value `json:"payload"`
}

type serverEnvelope struct {
	Type    string        `json:"type"`
	Payload ServerMessage `json:"payload"`
}

// The causes of a malformed message.
const (
	causeInvalidJSON       = "invalid-json"
	causeDuplicateField    = "duplicate-field"
	causePayloadNotObject  = "payload-not-object"
	causeUnknownType       = "unknown-type"
	causeWrongFieldType    = "wrong-field-type"
	causeMissingField      = "missing-field"
	causeInvalidFieldValue = "invalid-field-value"
)

var problems = map[string]string{
	causeInvalidJSON:       "the message is not valid JSON",
	causeDuplicateField:    "the message has a duplicate field",
	causePayloadNotObject:  "the payload is not an object",
	causeUnknownType:       "the message type is unknown",
	causeWrongFieldType:    "a field has the wrong type",
	causeMissingField:      "a required field is missing",
	causeInvalidFieldValue: "a field has an invalid value",
}

// ParseError is the error that [Parse] returns for a malformed message.
type ParseError struct {
	// Cause names the problem.
	Cause string
	// Field is the JSON pointer of the field at fault, or "" if there is none.
	Field string
}

func (e *ParseError) Error() string {
	return problems[e.Cause]
}

// ErrorMessage returns the voice.error that answers the malformed message.
func (e *ParseError) ErrorMessage() ErrorMessage {
	return ErrorMessage{
		Reason:   ErrorReasonInvalidMessage,
		Message:  e.Error() + "; fix the message and send it again",
		Metadata: map[string]string{"cause": e.Cause, "field": e.Field},
	}
}

// Parse reads a client message. A missing or null payload counts as {}, and
// unknown fields are ignored. If the message is malformed, the error is a
// [*ParseError].
func Parse(data []byte) (ClientMessage, error) {
	var env clientEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, unmarshalError(err, "")
	}

	payload := env.Payload
	switch payload.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		payload = jsontext.Value("{}")
	case jsontext.KindBeginObject:
	default:
		return nil, &ParseError{Cause: causePayloadNotObject, Field: "/payload"}
	}

	switch env.Type {
	case typeJoin:
		return Join{}, nil
	case typeAnswer:
		return parseAnswer(payload)
	case typeCandidate:
		return parseCandidate(payload)
	case typeRestartICE:
		return RestartICE{}, nil
	case typeSetMuted:
		return parseSetMuted(payload)
	case typeLeave:
		return Leave{}, nil
	default:
		return nil, &ParseError{Cause: causeUnknownType, Field: "/type"}
	}
}

func parseAnswer(payload jsontext.Value) (ClientMessage, error) {
	var a Answer
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, unmarshalError(err, "/payload")
	}
	if a.SDP == "" {
		return nil, &ParseError{Cause: causeMissingField, Field: "/payload/sdp"}
	}
	if a.OfferID < 1 {
		return nil, &ParseError{Cause: causeInvalidFieldValue, Field: "/payload/offerId"}
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
		return nil, unmarshalError(err, "/payload")
	}
	if c.Candidate == nil {
		return nil, &ParseError{Cause: causeMissingField, Field: "/payload/candidate"}
	}
	if *c.Candidate != "" && c.SDPMid == nil && c.SDPMLineIndex == nil {
		// Either field would do. The client is pointed at sdpMid.
		return nil, &ParseError{Cause: causeMissingField, Field: "/payload/sdpMid"}
	}
	return Candidate{
		Candidate:        *c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}, nil
}

func parseSetMuted(payload jsontext.Value) (ClientMessage, error) {
	// A bool cannot tell a missing field from false.
	var m struct {
		Muted *bool `json:"muted"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, unmarshalError(err, "/payload")
	}
	if m.Muted == nil {
		return nil, &ParseError{Cause: causeMissingField, Field: "/payload/muted"}
	}
	return SetMuted{Muted: *m.Muted}, nil
}

// unmarshalError describes an error from [json.Unmarshal] without the details
// of the Go types. pointer is where the unmarshaled value is in the message.
func unmarshalError(err error, pointer string) *ParseError {
	var semantic *json.SemanticError
	switch {
	case errors.As(err, &semantic):
		return &ParseError{Cause: causeWrongFieldType, Field: pointer + string(semantic.JSONPointer)}
	case errors.Is(err, jsontext.ErrDuplicateName):
		return &ParseError{Cause: causeDuplicateField}
	default:
		return &ParseError{Cause: causeInvalidJSON}
	}
}

// Marshal encodes msg in an envelope with its type and payload. Maps are
// encoded sorted by key.
func Marshal(msg ServerMessage) ([]byte, error) {
	return json.Marshal(serverEnvelope{Type: msg.messageType(), Payload: msg}, json.Deterministic(true))
}
