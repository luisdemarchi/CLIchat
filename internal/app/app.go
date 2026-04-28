package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/provider"
	"agent-chat-local/internal/session"
	"agent-chat-local/internal/terminal"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

type App struct {
	ctx       context.Context
	registry  *session.Registry
	providers []provider.Provider
	mirror    *mirror.Server
	terminals *terminal.Manager
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

	return &App{
		registry:  registry,
		providers: providers,
		mirror:    mirror.NewServer(),
		terminals: terminal.NewManager(),
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
	if !item.Available {
		return session.Session{}, fmt.Errorf("%s CLI not found", item.Name)
	}

	created := a.registry.Create(input, item)
	process, err := a.terminals.Start(terminal.StartOptions{
		SessionID: created.ID,
		Command:   item.Command,
		CWD:       created.CWD,
		OnOutput:  a.handleTerminalOutput,
		OnExit:    a.handleTerminalExit,
	})
	if err != nil {
		_, _ = a.registry.SetStatus(created.ID, session.Offline, "")
		updated, _ := a.registry.AppendSystem(created.ID, "Falha ao iniciar "+item.CLI+": "+err.Error())
		a.emitState()
		return updated, err
	}

	created, _ = a.registry.SetTerminal(created.ID, true, process.PID())
	created, _ = a.registry.AppendSystem(created.ID, fmt.Sprintf("Processo iniciado: %s (pid %d).", item.Command, process.PID()))
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
	if _, err := a.registry.SetStatus(input.SessionID, session.Busy, "terminal"); err != nil {
		return session.Session{}, err
	}
	if err := a.terminals.SendLine(input.SessionID, text); err != nil {
		_, _ = a.registry.SetStatus(input.SessionID, session.Offline, "")
		updated, _ := a.registry.AppendSystem(input.SessionID, "Nao foi possivel enviar para o terminal: "+err.Error())
		a.emitState()
		return updated, err
	}

	updated, _ := a.registry.Get(input.SessionID)
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

func (a *App) handleTerminalOutput(sessionID string, raw string) {
	text := cleanTerminalText(raw)
	if text == "" {
		return
	}
	if len(text) > 4000 {
		text = text[:4000] + "\n[saida truncada]"
	}
	_, _ = a.registry.AppendAssistant(sessionID, text)
	_, _ = a.registry.SetStatus(sessionID, session.Idle, "")
	a.emitState()
}

func (a *App) handleTerminalExit(sessionID string, err error) {
	a.terminals.Forget(sessionID)
	_, _ = a.registry.SetTerminal(sessionID, false, 0)
	status := "Processo encerrado."
	if err != nil {
		status = "Processo encerrado: " + err.Error()
	}
	_, _ = a.registry.AppendSystem(sessionID, status)
	_, _ = a.registry.SetStatus(sessionID, session.Offline, "")
	a.emitState()
}

func cleanTerminalText(raw string) string {
	cleaned := ansiPattern.ReplaceAllString(raw, "")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
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
