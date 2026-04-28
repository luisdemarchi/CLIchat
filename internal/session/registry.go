package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent-chat-local/internal/provider"
)

type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	selected string
}

func NewRegistry() *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
	}
}

func (r *Registry) Seed(providers []provider.Provider) {
	if len(r.List()) > 0 {
		return
	}

	for _, item := range providers {
		title := fmt.Sprintf("%s local", item.Name)
		session := r.Create(CreateInput{
			ProviderID: item.ID,
			Title:      title,
			CWD:        "",
		}, item)
		r.AppendSystem(session.ID, "Sessao pronta para conectar ao CLI "+item.CLI+".")
	}
}

func (r *Registry) Create(input CreateInput, item provider.Provider) Session {
	now := timestamp()
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = fmt.Sprintf("Novo chat %s", item.Name)
	}

	id := newID()
	session := &Session{
		ID:             id,
		Title:          title,
		ProviderID:     item.ID,
		ProviderTag:    item.Tag,
		ProviderAccent: item.Accent,
		Status:         Idle,
		CWD:            strings.TrimSpace(input.CWD),
		AvatarLabel:    avatarLabel(title, item.Name),
		LastMessage:    "Aguardando primeira mensagem.",
		ExternalAttach: fmt.Sprintf("agentctl attach %s", id),
		CreatedAt:      now,
		UpdatedAt:      now,
		Messages: []Message{
			{
				ID:        newID(),
				Role:      System,
				Text:      "Chat criado. O proximo passo e conectar este estado a um PTY persistente.",
				CreatedAt: now,
			},
		},
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[id] = session
	r.selected = id
	return clone(*session)
}

func (r *Registry) List() []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]Session, 0, len(r.sessions))
	for _, session := range r.sessions {
		items = append(items, clone(*session))
	}
	return items
}

func (r *Registry) Get(id string) (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	session, ok := r.sessions[id]
	if !ok {
		return Session{}, false
	}
	return clone(*session), true
}

func (r *Registry) Select(id string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[id]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	r.selected = id
	return clone(*session), nil
}

func (r *Registry) Selected() (Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.selected != "" {
		if session, ok := r.sessions[r.selected]; ok {
			return clone(*session), true
		}
	}

	var newest *Session
	for _, session := range r.sessions {
		if newest == nil || session.CreatedAt > newest.CreatedAt {
			newest = session
		}
	}
	if newest == nil {
		return Session{}, false
	}
	return clone(*newest), true
}

func (r *Registry) AppendUser(sessionID string, text string) (Session, error) {
	return r.appendMessage(sessionID, Message{ID: newID(), Role: User, Text: strings.TrimSpace(text), CreatedAt: timestamp()})
}

func (r *Registry) AppendAssistant(sessionID string, text string) (Session, error) {
	return r.appendMessage(sessionID, Message{ID: newID(), Role: Assistant, Text: strings.TrimSpace(text), CreatedAt: timestamp()})
}

func (r *Registry) AppendSystem(sessionID string, text string) (Session, error) {
	return r.appendMessage(sessionID, Message{ID: newID(), Role: System, Text: strings.TrimSpace(text), CreatedAt: timestamp()})
}

func (r *Registry) SetStatus(sessionID string, status Status, tool string) (Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[sessionID]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	session.Status = status
	session.CurrentTool = strings.TrimSpace(tool)
	session.UpdatedAt = timestamp()
	return clone(*session), nil
}

func (r *Registry) appendMessage(sessionID string, message Message) (Session, error) {
	if message.Text == "" {
		return Session{}, errors.New("message cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[sessionID]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	session.Messages = append(session.Messages, message)
	session.LastMessage = message.Text
	session.UpdatedAt = message.CreatedAt
	return clone(*session), nil
}

func clone(input Session) Session {
	input.Messages = append([]Message(nil), input.Messages...)
	return input
}

func newID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func avatarLabel(title string, fallback string) string {
	source := strings.TrimSpace(title)
	if source == "" {
		source = fallback
	}
	parts := strings.Fields(source)
	if len(parts) == 0 {
		return "AI"
	}
	if len(parts) == 1 {
		return strings.ToUpper(firstRune(parts[0]))
	}
	return strings.ToUpper(firstRune(parts[0]) + firstRune(parts[1]))
}

func firstRune(value string) string {
	for _, r := range value {
		return string(r)
	}
	return ""
}

func timestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
