package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Origin string

const (
	OriginInternal Origin = "internal"
	OriginExternal Origin = "external"
)

type Status string

const (
	StatusIdle    Status = "idle"
	StatusBusy    Status = "busy"
	StatusWaiting Status = "waiting"
	StatusOffline Status = "offline"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

type Message struct {
	ID        string `json:"id"`
	Role      Role   `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	SourceID  string `json:"sourceId,omitempty"`
}

type PendingAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Input string `json:"input"`
}

type Instance struct {
	ID                string          `json:"id"`
	ProviderID        string          `json:"providerId"`
	Origin            Origin          `json:"origin"`
	Title             string          `json:"title"`
	Topic             string          `json:"topic,omitempty"`
	CWD               string          `json:"cwd"`
	TTY               string          `json:"tty,omitempty"`
	PID               int             `json:"pid,omitempty"`
	ProviderSessionID string          `json:"providerSessionId,omitempty"`
	ClaudeSessionID   string          `json:"claudeSessionId,omitempty"`
	TranscriptPath    string          `json:"transcriptPath,omitempty"`
	TranscriptOffset  int64           `json:"transcriptOffset,omitempty"`
	Status            Status          `json:"status"`
	CurrentTool       string          `json:"currentTool,omitempty"`
	LastMessage       string          `json:"lastMessage,omitempty"`
	PendingQuestion   string          `json:"pendingQuestion,omitempty"`
	PendingActions    []PendingAction `json:"pendingActions"`
	Messages          []Message       `json:"messages"`
	TerminalAttached  bool            `json:"terminalAttached"`
	CreatedAt         string          `json:"createdAt"`
	UpdatedAt         string          `json:"updatedAt"`
	ExternalAttachCmd string          `json:"externalAttachCmd,omitempty"`
}

type state struct {
	Instances []*Instance `json:"instances"`
}

type Listener func(events []Event)

type EventKind string

const (
	EventInstanceUpdated EventKind = "instance_updated"
	EventInstanceRemoved EventKind = "instance_removed"
	EventStateSnapshot   EventKind = "state"
)

type Event struct {
	Kind    EventKind `json:"kind"`
	ID      string    `json:"id,omitempty"`
	Payload any       `json:"payload,omitempty"`
}

type Store struct {
	mu        sync.RWMutex
	path      string
	instances map[string]*Instance
	listeners map[int]Listener
	nextSub   int
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		path:      path,
		instances: make(map[string]*Instance),
		listeners: make(map[int]Listener),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	store.markBootOffline()
	return store, nil
}

func (s *Store) markBootOffline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, inst := range s.instances {
		if inst.Origin != OriginInternal {
			continue
		}
		// Force offline + detach for ALL internal instances at boot. Without
		// this guard, instances that had stale TerminalAttached=true are
		// skipped by tryAutoReconnect and the user is left with a frozen
		// chat (no PTY, no respawn).
		if inst.Status != StatusOffline || inst.TerminalAttached {
			inst.Status = StatusOffline
			inst.TerminalAttached = false
			inst.UpdatedAt = timestamp()
			changed = true
		}
	}
	if changed {
		_ = s.persistLocked()
	}
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	for _, inst := range st.Instances {
		if inst == nil || inst.ID == "" {
			continue
		}
		if inst.Messages == nil {
			inst.Messages = []Message{}
		}
		if inst.PendingActions == nil {
			inst.PendingActions = []PendingAction{}
		}
		s.instances[inst.ID] = inst
	}
	return nil
}

func (s *Store) persistLocked() error {
	list := make([]*Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		list = append(list, inst)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt < list[j].CreatedAt })
	data, err := json.MarshalIndent(state{Instances: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Subscribe(listener Listener) (int, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.listeners[id] = listener
	return id, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}

func (s *Store) emitLocked(events ...Event) {
	if len(events) == 0 {
		return
	}
	listeners := make([]Listener, 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	go func() {
		for _, listener := range listeners {
			listener(events)
		}
	}()
}

func (s *Store) Snapshot() []Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Instance, 0, len(s.instances))
	for _, inst := range s.instances {
		list = append(list, *cloneInstance(inst))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
	return list
}

func (s *Store) Get(id string) (Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	return *cloneInstance(inst), true
}

func (s *Store) FindByClaudeSessionID(claudeSessionID string) (Instance, bool) {
	if claudeSessionID == "" {
		return Instance{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.ClaudeSessionID == claudeSessionID {
			return *cloneInstance(inst), true
		}
	}
	return Instance{}, false
}

func (s *Store) FindByTranscriptPath(path string) (Instance, bool) {
	if path == "" {
		return Instance{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.TranscriptPath == path {
			return *cloneInstance(inst), true
		}
	}
	return Instance{}, false
}

func (s *Store) FindByProviderTranscript(providerID string, path string) (Instance, bool) {
	if path == "" {
		return Instance{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.ProviderID == providerID && inst.TranscriptPath == path {
			return *cloneInstance(inst), true
		}
	}
	return Instance{}, false
}

func (s *Store) FindByProviderSessionID(providerID string, providerSessionID string) (Instance, bool) {
	providerSessionID = strings.TrimSpace(providerSessionID)
	if providerID == "" || providerSessionID == "" {
		return Instance{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inst := range s.instances {
		if inst.ProviderID == providerID && inst.ProviderSessionID == providerSessionID {
			return *cloneInstance(inst), true
		}
	}
	return Instance{}, false
}

// FindAwaitingTranscript looks for an internal instance that
//   - matches `providerID`,
//   - has no `TranscriptPath` linked yet,
//   - has an attached terminal whose most recent update is no earlier than
//     `since` (important when an old chat is transferred into a fresh terminal),
//     and
//   - has matching `cwd` (exact, parent or child path) — only when `cwd` is
//     non-empty on both sides. If either side is empty we DO NOT match, to
//     prevent the previous bug where any rollout would land in any chat.
func (s *Store) FindAwaitingTranscript(providerID string, cwd string, since time.Time) (Instance, bool) {
	if providerID == "" || strings.TrimSpace(cwd) == "" {
		return Instance{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	best := s.findAwaitingTranscriptLocked(providerID, cwd, since)
	if best != nil {
		return *cloneInstance(best), true
	}
	return Instance{}, false
}

func (s *Store) findAwaitingTranscriptLocked(providerID string, cwd string, since time.Time) *Instance {
	var best *Instance
	var bestDistance time.Duration
	for _, inst := range s.instances {
		if inst.Origin != OriginInternal || inst.ProviderID != providerID || inst.TranscriptPath != "" {
			continue
		}
		if !inst.TerminalAttached {
			continue
		}
		if strings.TrimSpace(inst.CWD) == "" {
			continue
		}
		if !cwdMatches(inst.CWD, cwd) {
			continue
		}
		matchedAt, err := transcriptMatchTime(inst)
		if !since.IsZero() {
			if err == nil && matchedAt.Before(since) {
				continue
			}
		}
		distance := time.Duration(0)
		if err == nil && !since.IsZero() {
			sessionStarted := since.Add(2 * time.Minute)
			distance = matchedAt.Sub(sessionStarted)
			if distance < 0 {
				distance = -distance
			}
		}
		if best == nil || distance < bestDistance {
			best = inst
			bestDistance = distance
		}
	}
	if best != nil {
		return best
	}
	return nil
}

func transcriptMatchTime(inst *Instance) (time.Time, error) {
	if inst == nil {
		return time.Time{}, errors.New("instance is nil")
	}
	if inst.TerminalAttached && strings.TrimSpace(inst.UpdatedAt) != "" {
		return time.Parse(time.RFC3339Nano, inst.UpdatedAt)
	}
	return time.Parse(time.RFC3339Nano, inst.CreatedAt)
}

// ClaimTranscriptForInternal links a provider transcript to the matching
// app-managed chat. If the watcher had already created a duplicate external
// instance for the same transcript, that duplicate is removed so future polling
// and UI state resolve to the real chat.
func (s *Store) ClaimTranscriptForInternal(providerID string, path string, providerSessionID string, cwd string, since time.Time) (Instance, bool) {
	providerID = strings.TrimSpace(providerID)
	path = strings.TrimSpace(path)
	if providerID == "" || path == "" {
		return Instance{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var externalID string
	for _, inst := range s.instances {
		if inst.ProviderID != providerID || inst.TranscriptPath != path {
			continue
		}
		if inst.Origin == OriginInternal {
			return *cloneInstance(inst), true
		}
		if inst.Origin == OriginExternal {
			externalID = inst.ID
		}
	}

	inst := s.findAwaitingTranscriptLocked(providerID, cwd, since)
	if inst == nil {
		return Instance{}, false
	}
	inst.TranscriptPath = path
	inst.TranscriptOffset = 0
	if strings.TrimSpace(providerSessionID) != "" {
		inst.ProviderSessionID = strings.TrimSpace(providerSessionID)
	}
	inst.UpdatedAt = timestamp()
	events := []Event{{Kind: EventInstanceUpdated, ID: inst.ID, Payload: *cloneInstance(inst)}}
	if externalID != "" {
		delete(s.instances, externalID)
		events = append(events, Event{Kind: EventInstanceRemoved, ID: externalID})
	}
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(events...)
	return clone, true
}

func cwdMatches(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if a == b {
		return true
	}
	return isDirectChild(a, b) || isDirectChild(b, a)
}

func isDirectChild(parent string, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !strings.Contains(rel, string(filepath.Separator))
}

// UpdateCWD overwrites the CWD on an instance (used after start-terminal so
// the JSONL→instance linker has the real working directory to match against).
func (s *Store) UpdateCWD(id string, cwd string) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return
	}
	if inst.CWD == cwd {
		return
	}
	inst.CWD = cwd
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
}

// SetTopic overwrites the short "what am I doing right now" string for an
// instance. Called by the MCP tool `agent_chat_set_topic`. Topic is shown as
// the chat name in the sidebar/header and mirrored into Title so integrations
// that only read title also see the current subject.
func (s *Store) SetTopic(id string, topic string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	topic = strings.TrimSpace(topic)
	if len(topic) > 120 {
		topic = topic[:120]
	}
	inst.Topic = topic
	if topic != "" {
		inst.Title = topic
	}
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) SetTranscriptPath(id string, path string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if inst.TranscriptPath != path {
		inst.TranscriptOffset = 0
	}
	inst.TranscriptPath = path
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) SetTranscriptOffset(id string, path string, offset int64) {
	if offset < 0 {
		offset = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return
	}
	if path != "" && inst.TranscriptPath != path {
		return
	}
	if inst.TranscriptOffset == offset {
		return
	}
	inst.TranscriptOffset = offset
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
}

func (s *Store) SetProvider(id string, providerID string) (Instance, bool) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return Instance{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if inst.ProviderID == providerID {
		return *cloneInstance(inst), true
	}
	inst.ProviderID = providerID
	inst.ProviderSessionID = ""
	if providerID == "gemini" {
		inst.ProviderSessionID = instanceUUID(inst.ID)
	}
	inst.ClaudeSessionID = ""
	inst.TranscriptPath = ""
	inst.TranscriptOffset = 0
	inst.CurrentTool = ""
	inst.PendingQuestion = ""
	inst.PendingActions = []PendingAction{}
	inst.TerminalAttached = false
	inst.Status = StatusOffline
	inst.PID = 0
	inst.TTY = ""
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

type CreateInternalInput struct {
	ProviderID string
	Title      string
	CWD        string
}

func (s *Store) CreateInternal(input CreateInternalInput) Instance {
	now := timestamp()
	id := newID()
	inst := &Instance{
		ID:             id,
		ProviderID:     input.ProviderID,
		Origin:         OriginInternal,
		Title:          firstNonEmpty(input.Title, fmt.Sprintf("New %s chat", input.ProviderID)),
		CWD:            input.CWD,
		Status:         StatusIdle,
		Messages:       []Message{},
		PendingActions: []PendingAction{},
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if input.ProviderID == "gemini" {
		inst.ProviderSessionID = instanceUUID(id)
	}
	s.mu.Lock()
	s.instances[inst.ID] = inst
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	s.mu.Unlock()
	return clone
}

type RegisterExternalInput struct {
	Title             string
	CWD               string
	TTY               string
	PID               int
	ProviderSessionID string
	ClaudeSessionID   string
	TranscriptPath    string
	ProviderID        string
}

func (s *Store) RegisterExternal(input RegisterExternalInput) Instance {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := timestamp()
	if input.TranscriptPath != "" {
		for _, inst := range s.instances {
			if inst.ProviderID == input.ProviderID && inst.TranscriptPath == input.TranscriptPath {
				if input.Title != "" {
					inst.Title = input.Title
				}
				if input.CWD != "" {
					inst.CWD = input.CWD
				}
				if input.TTY != "" {
					inst.TTY = input.TTY
				}
				if input.PID != 0 {
					inst.PID = input.PID
				}
				if input.ClaudeSessionID != "" {
					inst.ClaudeSessionID = input.ClaudeSessionID
				}
				if input.ProviderSessionID != "" {
					inst.ProviderSessionID = input.ProviderSessionID
				}
				if inst.Status == StatusOffline {
					inst.Status = StatusIdle
				}
				inst.UpdatedAt = now
				_ = s.persistLocked()
				clone := *cloneInstance(inst)
				s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
				return clone
			}
		}
	}

	if input.ClaudeSessionID != "" {
		for _, inst := range s.instances {
			if inst.ClaudeSessionID == input.ClaudeSessionID {
				if input.Title != "" {
					inst.Title = input.Title
				}
				if input.CWD != "" {
					inst.CWD = input.CWD
				}
				if input.TTY != "" {
					inst.TTY = input.TTY
				}
				if input.PID != 0 {
					inst.PID = input.PID
				}
				if input.ProviderSessionID != "" {
					inst.ProviderSessionID = input.ProviderSessionID
				}
				if input.TranscriptPath != "" {
					if inst.TranscriptPath != input.TranscriptPath {
						inst.TranscriptOffset = 0
					}
					inst.TranscriptPath = input.TranscriptPath
				}
				if inst.Status == StatusOffline {
					inst.Status = StatusIdle
				}
				inst.UpdatedAt = now
				_ = s.persistLocked()
				clone := *cloneInstance(inst)
				s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
				return clone
			}
		}
	}

	providerID := input.ProviderID
	if providerID == "" {
		providerID = "claude"
	}
	title := firstNonEmpty(input.Title, defaultExternalTitle(providerID, input.CWD))
	inst := &Instance{
		ID:                newID(),
		ProviderID:        providerID,
		Origin:            OriginExternal,
		Title:             title,
		CWD:               input.CWD,
		TTY:               input.TTY,
		PID:               input.PID,
		ProviderSessionID: input.ProviderSessionID,
		ClaudeSessionID:   input.ClaudeSessionID,
		TranscriptPath:    input.TranscriptPath,
		Status:            StatusIdle,
		Messages:          []Message{},
		PendingActions:    []PendingAction{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.instances[inst.ID] = inst
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone
}

func (s *Store) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return
	}
	delete(s.instances, id)
	_ = s.persistLocked()
	s.emitLocked(Event{Kind: EventInstanceRemoved, ID: id})
}

func (s *Store) MarkOffline(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return
	}
	inst.Status = StatusOffline
	inst.TerminalAttached = false
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: *cloneInstance(inst)})
}

type StatusInput struct {
	Status Status
	Tool   string
}

func (s *Store) SetStatus(id string, input StatusInput) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if input.Status != "" {
		inst.Status = input.Status
	}
	inst.CurrentTool = strings.TrimSpace(input.Tool)
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) SetTerminalAttached(id string, attached bool, pid int) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	inst.TerminalAttached = attached
	if pid != 0 {
		inst.PID = pid
	}
	if !attached {
		inst.Status = StatusOffline
		inst.PID = 0
		inst.TTY = ""
	}
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) MarkTerminalExited(id string, pid int) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if pid != 0 && inst.PID != 0 && inst.PID != pid {
		return *cloneInstance(inst), true
	}
	inst.TerminalAttached = false
	inst.Status = StatusOffline
	inst.PID = 0
	inst.TTY = ""
	inst.CurrentTool = ""
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

type AttachClaudeInput struct {
	ClaudeSessionID string
	TranscriptPath  string
	TTY             string
	PID             int
	CWD             string
}

// AttachClaude links a Claude Code session to an existing instance (typically internal,
// created by the app). Used when the SessionStart hook fires inside a Claude process
// that the app spawned — we want to enrich the existing instance instead of creating
// a duplicate external one.
func (s *Store) AttachClaude(id string, input AttachClaudeInput) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if input.ClaudeSessionID != "" {
		inst.ClaudeSessionID = input.ClaudeSessionID
	}
	if input.TranscriptPath != "" {
		if inst.TranscriptPath != input.TranscriptPath {
			inst.TranscriptOffset = 0
		}
		inst.TranscriptPath = input.TranscriptPath
	}
	if input.TTY != "" {
		inst.TTY = input.TTY
	}
	if input.PID != 0 {
		inst.PID = input.PID
	}
	if input.CWD != "" && inst.CWD == "" {
		inst.CWD = input.CWD
	}
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) SetProviderSessionID(id string, providerSessionID string) (Instance, bool) {
	providerSessionID = strings.TrimSpace(providerSessionID)
	if providerSessionID == "" {
		return Instance{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	if inst.ProviderSessionID == providerSessionID {
		return *cloneInstance(inst), true
	}
	inst.ProviderSessionID = providerSessionID
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) SetExternalAttachCmd(id string, cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return
	}
	inst.ExternalAttachCmd = cmd
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
}

type AppendInput struct {
	Role     Role
	Text     string
	SourceID string
}

func (s *Store) AppendMessage(id string, input AppendInput) (Instance, bool, bool) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return Instance{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false, false
	}
	preview := text
	if len(preview) > 60 {
		preview = preview[:60]
	}
	log.Printf("AppendMessage id=%s provider=%s role=%s text=%q", shortID(id), inst.ProviderID, input.Role, preview)
	// Dedup against the last 30 messages to absorb the case where the same message
	// arrives both via direct API (user/assistant) and via the JSONL transcript watcher.
	if input.SourceID != "" {
		for _, existing := range inst.Messages {
			if existing.SourceID == input.SourceID {
				clone := *cloneInstance(inst)
				return clone, false, true
			}
		}
		limit := len(inst.Messages)
		if limit > 30 {
			limit = 30
		}
		for i := len(inst.Messages) - limit; i < len(inst.Messages); i++ {
			existing := inst.Messages[i]
			if existing.Role == input.Role && strings.TrimSpace(existing.Text) == text {
				clone := *cloneInstance(inst)
				return clone, false, true
			}
		}
	} else {
		limit := len(inst.Messages)
		if limit > 30 {
			limit = 30
		}
		for i := len(inst.Messages) - limit; i < len(inst.Messages); i++ {
			existing := inst.Messages[i]
			if existing.SourceID == "" && existing.Role == input.Role && strings.TrimSpace(existing.Text) == text {
				clone := *cloneInstance(inst)
				return clone, false, true
			}
		}
	}
	msg := Message{
		ID:        newID(),
		Role:      input.Role,
		Text:      text,
		CreatedAt: timestamp(),
		SourceID:  input.SourceID,
	}
	inst.Messages = append(inst.Messages, msg)
	inst.LastMessage = text
	inst.UpdatedAt = msg.CreatedAt
	if input.Role == RoleUser {
		topic := SmartTopic(inst.Messages, inst.Topic)
		if topic != "" {
			inst.Topic = topic
			inst.Title = topic
		}
		// User responded, clear any pending prompts
		inst.PendingQuestion = ""
		inst.PendingActions = []PendingAction{}
	}
	if input.Role == RoleAssistant {
		inst.Status = StatusIdle
		// Do NOT blindly clear pending actions here anymore.
		// They will be cleared by the next User message or if a new
		// assistant message arrives that doesn't contain a prompt.
		// (Logic moved to transcript watcher or handled by subsequent SetPending)
	}
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true, true
}

func (s *Store) SetPending(id string, question string, actions []PendingAction) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	inst.PendingQuestion = strings.TrimSpace(question)
	inst.PendingActions = append([]PendingAction(nil), actions...)
	if question != "" {
		inst.Status = StatusWaiting
	}
	inst.UpdatedAt = timestamp()
	_ = s.persistLocked()
	clone := *cloneInstance(inst)
	s.emitLocked(Event{Kind: EventInstanceUpdated, ID: inst.ID, Payload: clone})
	return clone, true
}

func (s *Store) ClearPending(id string) (Instance, bool) {
	return s.SetPending(id, "", nil)
}

func cloneInstance(inst *Instance) *Instance {
	if inst == nil {
		return nil
	}
	copy := *inst
	copy.Messages = append([]Message(nil), inst.Messages...)
	if copy.Messages == nil {
		copy.Messages = []Message{}
	}
	copy.PendingActions = append([]PendingAction(nil), inst.PendingActions...)
	if copy.PendingActions == nil {
		copy.PendingActions = []PendingAction{}
	}
	return &copy
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func defaultExternalTitle(providerID string, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	label := providerLabel(providerID)
	if cwd == "" {
		return "External " + label
	}
	base := filepath.Base(cwd)
	if base == "." || base == "/" || base == "" {
		return "External " + label
	}
	return label + " · " + base
}

func providerLabel(providerID string) string {
	switch strings.ToLower(strings.TrimSpace(providerID)) {
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini"
	default:
		return "Claude"
	}
}

func instanceUUID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if len(id) != 32 {
		return id
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return id
		}
	}
	return id[0:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:32]
}
