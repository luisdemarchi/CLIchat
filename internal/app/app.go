package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/provider"
	"agent-chat-local/internal/session"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx       context.Context
	registry  *session.Registry
	providers []provider.Provider
	mirror    *mirror.Server
}

type Bootstrap struct {
	Providers []provider.Provider `json:"providers"`
	Sessions  []session.Session   `json:"sessions"`
	Selected  *session.Session    `json:"selected,omitempty"`
	Mirror    mirror.Status       `json:"mirror"`
}

func New() *App {
	providers := provider.Defaults()
	registry := session.NewRegistry()
	registry.Seed(providers)

	return &App{
		registry:  registry,
		providers: providers,
		mirror:    mirror.NewServer(),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.emitState()
}

func (a *App) GetBootstrap() Bootstrap {
	return a.bootstrap()
}

func (a *App) ListProviders() []provider.Provider {
	return a.providers
}

func (a *App) ListSessions() []session.Session {
	return a.registry.List()
}

func (a *App) SelectSession(id string) (session.Session, error) {
	selected, err := a.registry.Select(id)
	if err != nil {
		return session.Session{}, err
	}
	a.emitState()
	return selected, nil
}

func (a *App) CreateChat(input session.CreateInput) (session.Session, error) {
	item, ok := a.providerByID(input.ProviderID)
	if !ok {
		return session.Session{}, fmt.Errorf("unknown provider: %s", input.ProviderID)
	}
	created := a.registry.Create(input, item)
	a.emitState()
	return created, nil
}

func (a *App) SendMessage(input session.SendInput) (session.Session, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return session.Session{}, errors.New("message cannot be empty")
	}

	if _, err := a.registry.AppendUser(input.SessionID, text); err != nil {
		return session.Session{}, err
	}
	if _, err := a.registry.SetStatus(input.SessionID, session.Busy, "queued"); err != nil {
		return session.Session{}, err
	}

	reply := "Mensagem registrada. A proxima etapa e conectar este chat ao PTY do CLI para executar a conversa real."
	updated, err := a.registry.AppendAssistant(input.SessionID, reply)
	if err != nil {
		return session.Session{}, err
	}
	updated, err = a.registry.SetStatus(input.SessionID, session.Idle, "")
	if err != nil {
		return session.Session{}, err
	}

	a.emitState()
	return updated, nil
}

func (a *App) ExternalAttachCommand(sessionID string) (string, error) {
	item, ok := a.registry.Get(sessionID)
	if !ok {
		return "", errors.New("session not found")
	}
	return item.ExternalAttach, nil
}

func (a *App) MirrorStatus() mirror.Status {
	return a.mirror.Status()
}

func (a *App) providerByID(id provider.ID) (provider.Provider, bool) {
	for _, item := range a.providers {
		if item.ID == id {
			return item, true
		}
	}
	return provider.Provider{}, false
}

func (a *App) bootstrap() Bootstrap {
	var selected *session.Session
	if item, ok := a.registry.Selected(); ok {
		selected = &item
	}
	return Bootstrap{
		Providers: a.providers,
		Sessions:  a.registry.List(),
		Selected:  selected,
		Mirror:    a.mirror.Status(),
	}
}

func (a *App) emitState() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "state:update", a.bootstrap())
}
