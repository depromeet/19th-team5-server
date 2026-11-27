package protocol

import (
	"encoding/json/v2"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want ClientMessage
	}{
		{
			name: "join_null_payload",
			in:   `{"type":"voice.join","payload":null}`,
			want: Join{},
		},
		{
			name: "answer_unknown_fields",
			in:   `{"type":"voice.answer","id":"m1","payload":{"sdp":"v=0","offerId":1,"extra":true}}`,
			want: Answer{SDP: "v=0", OfferID: 1},
		},
		{
			name: "candidate",
			in: `{"type":"voice.candidate","payload":{
				"candidate":"candidate:1 1 udp 2122260223 192.0.2.1 54321 typ host",
				"sdpMid":"0","sdpMLineIndex":0,"usernameFragment":"abcd"}}`,
			want: Candidate{
				Candidate:        "candidate:1 1 udp 2122260223 192.0.2.1 54321 typ host",
				SDPMid:           new("0"),
				SDPMLineIndex:    new(uint16(0)),
				UsernameFragment: new("abcd"),
			},
		},
		{
			name: "candidate_only_sdpMLineIndex",
			in:   `{"type":"voice.candidate","payload":{"candidate":"candidate:1","sdpMid":null,"sdpMLineIndex":1}}`,
			want: Candidate{Candidate: "candidate:1", SDPMLineIndex: new(uint16(1))},
		},
		{
			name: "end_of_candidates",
			in:   `{"type":"voice.candidate","payload":{"candidate":""}}`,
			want: Candidate{Candidate: ""},
		},
		{
			name: "set_muted",
			in:   `{"type":"voice.setMuted","payload":{"muted":false}}`,
			want: SetMuted{Muted: false},
		},
		{
			name: "leave_missing_payload",
			in:   `{"type":"voice.leave"}`,
			want: Leave{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.in, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Parse(%q) returned diff (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want *ParseError
	}{
		{name: "unknown_type", in: `{"type":"voice.unknown"}`, want: &ParseError{Cause: causeUnknownType, Field: "/type"}},
		{name: "duplicate_field", in: `{"type":"voice.answer","payload":{"sdp":"v=0","sdp":"v=1","offerId":1}}`, want: &ParseError{Cause: causeDuplicateField}},
		{name: "invalid_utf8", in: "{\"type\":\"voice.answer\",\"payload\":{\"sdp\":\"v=0\xff\",\"offerId\":1}}", want: &ParseError{Cause: causeInvalidJSON}},
		{name: "array_payload", in: `{"type":"voice.join","payload":[]}`, want: &ParseError{Cause: causePayloadNotObject, Field: "/payload"}},

		{name: "answer_uppercase_field", in: `{"type":"voice.answer","payload":{"SDP":"v=0","offerId":1}}`, want: &ParseError{Cause: causeMissingField, Field: "/payload/sdp"}},
		{name: "answer_missing_offerId", in: `{"type":"voice.answer","payload":{"sdp":"v=0"}}`, want: &ParseError{Cause: causeInvalidFieldValue, Field: "/payload/offerId"}},
		{name: "answer_string_offerId", in: `{"type":"voice.answer","payload":{"sdp":"v=0","offerId":"1"}}`, want: &ParseError{Cause: causeWrongFieldType, Field: "/payload/offerId"}},

		{name: "candidate_missing_candidate", in: `{"type":"voice.candidate","payload":{"sdpMid":"0","sdpMLineIndex":0}}`, want: &ParseError{Cause: causeMissingField, Field: "/payload/candidate"}},
		{name: "candidate_number_candidate", in: `{"type":"voice.candidate","payload":{"candidate":1,"sdpMid":"0"}}`, want: &ParseError{Cause: causeWrongFieldType, Field: "/payload/candidate"}},
		{
			name: "candidate_null_sdpMid_and_sdpMLineIndex",
			in:   `{"type":"voice.candidate","payload":{"candidate":"candidate:1","sdpMid":null,"sdpMLineIndex":null}}`,
			want: &ParseError{Cause: causeMissingField, Field: "/payload/sdpMid"},
		},

		{name: "set_muted_missing_muted", in: `{"type":"voice.setMuted"}`, want: &ParseError{Cause: causeMissingField, Field: "/payload/muted"}},
		{name: "set_muted_string_muted", in: `{"type":"voice.setMuted","payload":{"muted":"true"}}`, want: &ParseError{Cause: causeWrongFieldType, Field: "/payload/muted"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			if diff := cmp.Diff(tt.want, err); diff != "" {
				t.Errorf("Parse(%q) error diff (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

func TestParseErrorErrorMessage(t *testing.T) {
	e := &ParseError{Cause: causeMissingField, Field: "/payload/sdp"}
	want := ErrorMessage{
		Reason:   ErrorReasonInvalidMessage,
		Message:  "a required field is missing; fix the message and send it again",
		Metadata: map[string]string{"cause": "missing-field", "field": "/payload/sdp"},
	}
	if diff := cmp.Diff(want, e.ErrorMessage()); diff != "" {
		t.Errorf("%#v.ErrorMessage() diff (-want +got):\n%s", e, diff)
	}
}

func TestMarshal(t *testing.T) {
	tests := []struct {
		name string
		msg  ServerMessage
		want string
	}{
		{
			name: "offer",
			msg:  Offer{SDP: "v=0\r\n...", OfferID: 1},
			want: `{"type":"voice.offer","payload":{"sdp":"v=0\r\n...","offerId":1}}`,
		},
		{
			name: "muted",
			msg:  Muted{ParticipantID: "p1", Muted: true},
			want: `{"type":"voice.muted","payload":{"participantId":"p1","muted":true}}`,
		},
		{
			name: "error_design_doc",
			msg: ErrorMessage{
				Reason:   ErrorReasonInvalidMessage,
				Message:  "a field has the wrong type; fix the message and send it again",
				Metadata: map[string]string{"cause": "wrong-field-type", "field": "/payload/offerId"},
			},
			want: `{"type":"voice.error","payload":{"reason":"INVALID_MESSAGE","message":"a field has the wrong type; fix the message and send it again","metadata":{"cause":"wrong-field-type","field":"/payload/offerId"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal(%#v) returned error: %v", tt.msg, err)
			}
			var got, want any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal(%s) returned error: %v", data, err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatalf("json.Unmarshal(%s) returned error: %v", tt.want, err)
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Marshal(%#v) returned diff (-want +got):\n%s", tt.msg, diff)
			}
		})
	}
}
