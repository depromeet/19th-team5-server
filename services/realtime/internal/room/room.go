// Package room manages interview rooms, WebSocket sessions, and message routing.
package room

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrRoomNotOpen reports that an interview room has not opened yet or has
// already ended.
var ErrRoomNotOpen = errors.New("room is not open")

// Role is a participant's role in an interview room.
type Role string

// Participant roles in an interview room.
const (
	RoleInterviewer Role = "INTERVIEWER"
	RoleInterviewee Role = "INTERVIEWEE"
)

// State is a participant's connection state in an interview room.
type State string

// Participant connection states in an interview room.
const (
	StateConnected    State = "CONNECTED"
	StateReconnecting State = "RECONNECTING"
)

// Member is a reserved participant returned by core for an interview room.
type Member struct {
	MemberID string
	Nickname string
	Role     Role
}

// ParticipantStore returns the reserved participants of an interview room.
// It returns [ErrRoomNotOpen] if the room has not opened yet or has ended.
type ParticipantStore interface {
	Participants(ctx context.Context, interviewID string) ([]Member, error)
}

// VoiceRoom handles voice.* signaling messages for one interview room.
type VoiceRoom interface {
	// Receive handles a serialized voice.* message from participantID without blocking.
	Receive(ctx context.Context, participantID string, data []byte)
	// Leave removes participantID from voice signaling and closes its media connection.
	Leave(ctx context.Context, participantID string)
	// Disconnect notifies voice signaling that participantID's WebSocket was lost.
	Disconnect(ctx context.Context, participantID string)
	// Reconnect notifies voice signaling that participantID's WebSocket is back.
	Reconnect(ctx context.Context, participantID string)
}

// VoiceRoomFactory creates a [VoiceRoom] for a new interview room. send
// delivers a serialized voice.* message to participantID without blocking.
type VoiceRoomFactory func(send func(ctx context.Context, participantID string, msg []byte)) VoiceRoom

const (
	defaultGracePeriod         = 30 * time.Second
	defaultParticipantsTimeout = 5 * time.Second
)

const (
	closeNormal        = 1000
	closeGoingAway     = 1001
	closeTryAgainLater = 1013
	closeForbidden     = 4003
	closeReplaced      = 4009
	closeRoomNotOpen   = 4012
)

const (
	typeJoin         = "room.join"
	typeLeave        = "room.leave"
	typeJoined       = "room.joined"
	typeParticipants = "room.participants"
	typeError        = "room.error"
)

const errorReasonInvalidMessage = "INVALID_MESSAGE"

const (
	causeInvalidJSON      = "invalid-json"
	causeDuplicateField   = "duplicate-field"
	causePayloadNotObject = "payload-not-object"
	causeUnknownType      = "unknown-type"
	causeWrongFieldType   = "wrong-field-type"
	causeMissingField     = "missing-field"
	causeNotJoined        = "not-joined"
	causeAlreadyJoined    = "already-joined"
)

var errorProblems = map[string]string{
	causeInvalidJSON:      "the message is not valid JSON",
	causeDuplicateField:   "the message has a duplicate field",
	causePayloadNotObject: "the payload is not an object",
	causeUnknownType:      "the message type is unknown",
	causeWrongFieldType:   "a field has the wrong type",
	causeMissingField:     "a required field is missing",
	causeNotJoined:        "the connection has not joined a room yet",
	causeAlreadyJoined:    "the connection has already joined a room",
}

// conn is a client WebSocket connection seen by a room. Its methods must not
// block because the room goroutine calls them.
type conn interface {
	send(msg []byte)
	close(code int, reason string)
}

type clientEnvelope struct {
	Type    string         `json:"type"`
	Payload jsontext.Value `json:"payload"`
}

type serverEnvelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type joinPayload struct {
	InterviewID string `json:"interviewId"`
}

type joinedPayload struct {
	ParticipantID string `json:"participantId"`
}

type participantPayload struct {
	ParticipantID string `json:"participantId"`
	Nickname      string `json:"nickname"`
	Role          Role   `json:"role"`
	State         State  `json:"state"`
}

type participantsPayload struct {
	Participants []participantPayload `json:"participants"`
}

type errorPayload struct {
	Reason   string            `json:"reason"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

// parseError describes a malformed or out-of-state client message.
type parseError struct {
	cause string
	field string
}

func (e *parseError) payload() errorPayload {
	return errorPayload{
		Reason:   errorReasonInvalidMessage,
		Message:  errorProblems[e.cause] + "; fix the message and send it again",
		Metadata: map[string]string{"cause": e.cause, "field": e.field},
	}
}

func parseEnvelope(data []byte) (string, jsontext.Value, *parseError) {
	var env clientEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return "", nil, unmarshalError(err, "")
	}
	payload := env.Payload
	switch payload.Kind() {
	case jsontext.KindInvalid, jsontext.KindNull:
		payload = jsontext.Value("{}")
	case jsontext.KindBeginObject:
	default:
		return "", nil, &parseError{cause: causePayloadNotObject, field: "/payload"}
	}
	return env.Type, payload, nil
}

func parseJoin(payload jsontext.Value) (string, *parseError) {
	var p joinPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", unmarshalError(err, "/payload")
	}
	if p.InterviewID == "" {
		return "", &parseError{cause: causeMissingField, field: "/payload/interviewId"}
	}
	return p.InterviewID, nil
}

func unmarshalError(err error, pointer string) *parseError {
	var semantic *json.SemanticError
	switch {
	case errors.As(err, &semantic):
		return &parseError{cause: causeWrongFieldType, field: pointer + string(semantic.JSONPointer)}
	case errors.Is(err, jsontext.ErrDuplicateName):
		return &parseError{cause: causeDuplicateField}
	default:
		return &parseError{cause: causeInvalidJSON}
	}
}

func marshalServerMessage(typ string, payload any) ([]byte, error) {
	return json.Marshal(serverEnvelope{Type: typ, Payload: payload}, json.Deterministic(true))
}

type sessionState int

const (
	sessionUnjoined sessionState = iota
	sessionJoining
	sessionJoined
)

// session handles messages from one authenticated connection.
type session struct {
	hub      *hub
	memberID string
	conn     conn

	mu    sync.Mutex
	state sessionState
	room  *room
}

func (h *hub) newSession(memberID string, c conn) *session {
	return &session{
		hub:      h,
		memberID: memberID,
		conn:     c,
	}
}

// handleMessage processes one client message from the connection's read loop.
func (s *session) handleMessage(ctx context.Context, data []byte) {
	typ, payload, parseErr := parseEnvelope(data)
	if parseErr != nil {
		s.sendError(parseErr)
		return
	}
	switch typ {
	case typeJoin:
		s.handleJoin(ctx, payload)
	case typeLeave:
		s.handleLeave(ctx)
	default:
		if strings.HasPrefix(typ, "voice.") && s.hub.newVoiceRoom != nil {
			s.handleVoice(ctx, data)
			return
		}
		s.sendError(&parseError{cause: causeUnknownType, field: "/type"})
	}
}

func (s *session) handleVoice(ctx context.Context, data []byte) {
	s.mu.Lock()
	if s.state != sessionJoined {
		s.mu.Unlock()
		s.sendError(&parseError{cause: causeNotJoined})
		return
	}
	r := s.room
	s.mu.Unlock()

	voiceCtx := context.WithoutCancel(ctx)
	r.post(func() { r.handleVoiceMessage(voiceCtx, s.memberID, s.conn, data) })
}

func (s *session) handleJoin(ctx context.Context, payload jsontext.Value) {
	s.mu.Lock()
	if s.state != sessionUnjoined {
		s.mu.Unlock()
		s.sendError(&parseError{cause: causeAlreadyJoined})
		return
	}
	interviewID, parseErr := parseJoin(payload)
	if parseErr != nil {
		s.mu.Unlock()
		s.sendError(parseErr)
		return
	}
	s.state = sessionJoining
	s.mu.Unlock()

	r := s.hub.join(ctx, interviewID, s)
	s.mu.Lock()
	s.room = r
	s.mu.Unlock()
}

func (s *session) handleLeave(ctx context.Context) {
	s.mu.Lock()
	if s.state != sessionJoined {
		s.mu.Unlock()
		s.sendError(&parseError{cause: causeNotJoined})
		return
	}
	r := s.room
	s.mu.Unlock()

	leaveCtx := context.WithoutCancel(ctx)
	r.post(func() { r.handleLeave(leaveCtx, s.memberID, s.conn) })
}

func (s *session) markJoined(r *room) {
	s.mu.Lock()
	s.state = sessionJoined
	s.room = r
	s.mu.Unlock()
}

// disconnect notifies the session's room that the connection closed.
func (s *session) disconnect(ctx context.Context) {
	s.mu.Lock()
	r := s.room
	s.mu.Unlock()
	if r == nil {
		return
	}
	discCtx := context.WithoutCancel(ctx)
	r.post(func() { r.handleDisconnect(discCtx, s.memberID, s.conn) })
}

func (s *session) sendError(e *parseError) {
	data, err := marshalServerMessage(typeError, e.payload())
	if err != nil {
		s.hub.logger.Warn("marshal room.error failed", "err", err)
		return
	}
	s.conn.send(data)
}

// hub owns the map of active interview rooms.
// Lock order: hub.mu -> room.mu.
type hub struct {
	store               ParticipantStore
	newVoiceRoom        VoiceRoomFactory
	logger              *slog.Logger
	gracePeriod         time.Duration
	participantsTimeout time.Duration

	mu    sync.Mutex
	rooms map[string]*room
}

func newHub(store ParticipantStore, logger *slog.Logger) *hub {
	return &hub{
		store:               store,
		logger:              logger,
		gracePeriod:         defaultGracePeriod,
		participantsTimeout: defaultParticipantsTimeout,
		rooms:               make(map[string]*room),
	}
}

func (h *hub) join(ctx context.Context, interviewID string, s *session) *room {
	joinCtx := context.WithoutCancel(ctx)
	h.mu.Lock()
	r, ok := h.rooms[interviewID]
	created := false
	if !ok {
		r = newRoom(h, interviewID)
		h.rooms[interviewID] = r
		created = true
	}
	// Post while holding h.mu so removeIfEmpty cannot delete r before the join
	// is queued.
	r.post(func() { r.handleJoin(joinCtx, s) })
	h.mu.Unlock()

	if created {
		go h.fetchParticipants(joinCtx, r, interviewID)
	}
	return r
}

func (h *hub) fetchParticipants(ctx context.Context, r *room, interviewID string) {
	fetchCtx, cancel := context.WithTimeout(ctx, h.participantsTimeout)
	defer cancel()
	members, err := h.store.Participants(fetchCtx, interviewID)
	r.post(func() { r.handleParticipantsLoaded(ctx, members, err) })
}

// removeRoom unconditionally removes r from h.rooms if it is still registered.
func (h *hub) removeRoom(r *room) {
	h.mu.Lock()
	if h.rooms[r.interviewID] == r {
		delete(h.rooms, r.interviewID)
	}
	h.mu.Unlock()
}

// removeIfEmpty deletes r from h.rooms if r has no pending work in its queue
// and is still empty.
func (h *hub) removeIfEmpty(r *room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[r.interviewID] != r {
		return
	}
	r.mu.Lock()
	hasWork := len(r.queue) > 0
	r.mu.Unlock()
	if hasWork {
		return
	}
	delete(h.rooms, r.interviewID)
}

// shutdown cancels all grace timers and clears active rooms when the server
// shuts down.
func (h *hub) shutdown(ctx context.Context) {
	h.mu.Lock()
	rooms := make([]*room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	clear(h.rooms)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, r := range rooms {
		wg.Add(1)
		r.post(func() {
			defer wg.Done()
			r.shutdown(ctx)
		})
	}
	wg.Wait()
}

// room runs the state of one interview room on a single goroutine.
type room struct {
	hub         *hub
	interviewID string
	logger      *slog.Logger

	mu    sync.Mutex
	queue []func()
	busy  bool

	// The fields below are accessed only on the room goroutine.
	loading       bool
	loadErrCode   int
	loadErrReason string
	pendingJoins  []*session
	members       map[string]Member
	memberToPID   map[string]string
	pidToMember   map[string]string
	nextPID       int
	participants  map[string]*participant
	voice         VoiceRoom
}

type participant struct {
	pid           string
	seq           int
	member        Member
	state         State
	conn          conn
	disconnectGen int
	graceTimer    *time.Timer
}

func newRoom(h *hub, interviewID string) *room {
	r := &room{
		hub:          h,
		interviewID:  interviewID,
		logger:       h.logger.With("interviewId", interviewID),
		loading:      true,
		members:      make(map[string]Member),
		memberToPID:  make(map[string]string),
		pidToMember:  make(map[string]string),
		participants: make(map[string]*participant),
	}
	if h.newVoiceRoom != nil {
		r.voice = h.newVoiceRoom(r.sendToParticipant)
	}
	return r
}

func (r *room) sendToParticipant(_ context.Context, participantID string, msg []byte) {
	r.post(func() {
		memberID, ok := r.pidToMember[participantID]
		if !ok {
			return
		}
		p, ok := r.participants[memberID]
		if !ok || p.state != StateConnected || p.conn == nil {
			return
		}
		p.conn.send(msg)
	})
}

func (r *room) handleVoiceMessage(ctx context.Context, memberID string, c conn, data []byte) {
	p, ok := r.participants[memberID]
	if !ok || p.conn != c || r.voice == nil {
		return
	}
	r.voice.Receive(ctx, p.pid, data)
}

func (r *room) post(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queue = append(r.queue, f)
	if !r.busy {
		r.busy = true
		go r.run()
	}
}

func (r *room) run() {
	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.queue = nil
			r.busy = false
			r.mu.Unlock()
			return
		}
		f := r.queue[0]
		r.queue[0] = nil
		r.queue = r.queue[1:]
		r.mu.Unlock()
		f()
	}
}

func (r *room) handleJoin(ctx context.Context, s *session) {
	if r.loading {
		r.pendingJoins = append(r.pendingJoins, s)
		return
	}
	if r.loadErrCode != 0 {
		s.conn.close(r.loadErrCode, r.loadErrReason)
		return
	}
	r.applyJoin(ctx, s)
}

func (r *room) handleParticipantsLoaded(ctx context.Context, members []Member, err error) {
	r.loading = false
	pending := r.pendingJoins
	r.pendingJoins = nil

	if err != nil {
		code, reason := closeTryAgainLater, "Try Again Later"
		if errors.Is(err, ErrRoomNotOpen) {
			code, reason = closeRoomNotOpen, "ROOM_NOT_OPEN"
		}
		r.loadErrCode = code
		r.loadErrReason = reason
		r.hub.removeRoom(r)
		r.logger.WarnContext(ctx, "load participants failed", "err", err)
		for _, s := range pending {
			s.conn.close(code, reason)
		}
		return
	}

	for _, m := range members {
		r.members[m.MemberID] = m
	}
	for _, s := range pending {
		r.applyJoin(ctx, s)
	}
	if len(r.participants) == 0 {
		r.hub.removeIfEmpty(r)
	}
}

func (r *room) applyJoin(ctx context.Context, s *session) {
	member, ok := r.members[s.memberID]
	if !ok {
		s.conn.close(closeForbidden, "FORBIDDEN")
		return
	}

	pid, ok := r.memberToPID[s.memberID]
	if !ok {
		r.nextPID++
		pid = fmt.Sprintf("p%d", r.nextPID)
		r.memberToPID[s.memberID] = pid
		r.pidToMember[pid] = s.memberID
	}

	reconnected := false
	if existing, ok := r.participants[s.memberID]; ok {
		if existing.state == StateConnected {
			existing.conn.close(closeReplaced, "REPLACED")
		} else if existing.graceTimer != nil {
			existing.graceTimer.Stop()
			existing.graceTimer = nil
		}
		existing.state = StateConnected
		existing.conn = s.conn
		reconnected = true
	} else {
		r.participants[s.memberID] = &participant{
			pid:    pid,
			seq:    r.nextPID,
			member: member,
			state:  StateConnected,
			conn:   s.conn,
		}
	}

	s.markJoined(r)
	r.sendTo(ctx, s.conn, typeJoined, joinedPayload{ParticipantID: pid})
	r.broadcastParticipants(ctx)
	if reconnected && r.voice != nil {
		r.voice.Reconnect(ctx, pid)
	}
}

func (r *room) handleDisconnect(ctx context.Context, memberID string, c conn) {
	if r.loading {
		r.pendingJoins = slices.DeleteFunc(r.pendingJoins, func(s *session) bool {
			return s.conn == c
		})
		return
	}
	p, ok := r.participants[memberID]
	if !ok || p.conn != c {
		return
	}
	p.conn = nil
	p.state = StateReconnecting
	p.disconnectGen++
	gen := p.disconnectGen
	timerCtx := context.WithoutCancel(ctx)
	p.graceTimer = time.AfterFunc(r.hub.gracePeriod, func() {
		r.post(func() { r.handleGraceExpired(timerCtx, memberID, gen) })
	})
	if r.voice != nil {
		r.voice.Disconnect(ctx, p.pid)
	}
	r.broadcastParticipants(ctx)
}

func (r *room) handleGraceExpired(ctx context.Context, memberID string, gen int) {
	p, ok := r.participants[memberID]
	if !ok || p.state != StateReconnecting || p.disconnectGen != gen {
		return
	}
	delete(r.participants, memberID)
	if r.voice != nil {
		r.voice.Leave(ctx, p.pid)
	}
	r.broadcastParticipants(ctx)
	if len(r.participants) == 0 {
		r.hub.removeIfEmpty(r)
	}
}

func (r *room) handleLeave(ctx context.Context, memberID string, c conn) {
	p, ok := r.participants[memberID]
	if !ok || p.conn != c {
		return
	}
	delete(r.participants, memberID)
	if r.voice != nil {
		r.voice.Leave(ctx, p.pid)
	}
	c.close(closeNormal, "")
	r.broadcastParticipants(ctx)
	if len(r.participants) == 0 {
		r.hub.removeIfEmpty(r)
	}
}

func (r *room) broadcastParticipants(ctx context.Context) {
	list := make([]*participant, 0, len(r.participants))
	for _, p := range r.participants {
		list = append(list, p)
	}
	slices.SortFunc(list, func(a, b *participant) int {
		return cmp.Compare(a.seq, b.seq)
	})
	payload := participantsPayload{
		Participants: make([]participantPayload, 0, len(list)),
	}
	for _, p := range list {
		payload.Participants = append(payload.Participants, participantPayload{
			ParticipantID: p.pid,
			Nickname:      p.member.Nickname,
			Role:          p.member.Role,
			State:         p.state,
		})
	}
	data, err := marshalServerMessage(typeParticipants, payload)
	if err != nil {
		r.logger.WarnContext(ctx, "marshal room.participants failed", "err", err)
		return
	}
	for _, p := range list {
		if p.state == StateConnected && p.conn != nil {
			p.conn.send(data)
		}
	}
}

func (r *room) sendTo(ctx context.Context, c conn, typ string, payload any) {
	data, err := marshalServerMessage(typ, payload)
	if err != nil {
		r.logger.WarnContext(ctx, "marshal message failed", "type", typ, "err", err)
		return
	}
	c.send(data)
}

func (r *room) shutdown(ctx context.Context) {
	r.pendingJoins = nil
	for _, p := range r.participants {
		if p.graceTimer != nil {
			p.graceTimer.Stop()
			p.graceTimer = nil
		}
		if r.voice != nil {
			r.voice.Leave(ctx, p.pid)
		}
	}
	clear(r.participants)
}
