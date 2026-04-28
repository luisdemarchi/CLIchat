package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"agent-chat-local/internal/hostclient"
	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/provider"
	"agent-chat-local/internal/session"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)
var controlPattern = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

type App struct {
	ctx       context.Context
	registry  *session.Registry
	providers []provider.Provider
	host      *hostclient.Client
	outputs   *outputFilter
}

type TerminalInput struct {
	SessionID string `json:"sessionId"`
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
		host:      hostclient.New(""),
		outputs:   newOutputFilter(),
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
	created, _ = a.registry.SetExternalAttach(created.ID, "agent-host serve")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.host.Health(ctx); err != nil {
		created, _ = a.registry.SetWaiting(created.ID, "Inicie o host: agent-host serve")
		a.emitState()
		return created, nil
	}

	pid, err := a.startTerminalSession(created.ID, item, created.CWD)
	if err != nil {
		created, _ = a.registry.SetWaiting(created.ID, "agent-host online, mas o terminal nao iniciou: "+err.Error())
		a.emitState()
		return created, nil
	}

	created, _ = a.registry.SetTerminal(created.ID, true, pid)
	created, _ = a.registry.SetExternalAttach(created.ID, "agentctl attach "+created.ID)
	created, _ = a.registry.SetLastMessage(created.ID, fmt.Sprintf("%s pronto no terminal local.", item.Name))
	a.outputs.register(created.ID)
	a.subscribeHost(created.ID)
	a.emitState()
	return created, nil
}

func (a *App) OpenTerminal(input TerminalInput) (string, error) {
	item, ok := a.registry.Get(input.SessionID)
	if !ok {
		return "", errors.New("session not found")
	}
	if item.ExternalAttach == "" {
		return "", errors.New("terminal command not available")
	}
	wailsruntime.ClipboardSetText(a.ctx, item.ExternalAttach)
	return item.ExternalAttach, nil
}

func (a *App) SendMessage(input session.SendInput) (session.Session, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return session.Session{}, errors.New("message cannot be empty")
	}

	current, ok := a.registry.Get(input.SessionID)
	if !ok {
		return session.Session{}, errors.New("session not found")
	}
	if current.Status == session.Busy {
		return current, errors.New("aguarde a resposta atual terminar")
	}
	if _, err := a.registry.AppendUser(input.SessionID, text); err != nil {
		return session.Session{}, err
	}
	if _, err := a.registry.SetStatus(input.SessionID, session.Busy, "terminal"); err != nil {
		return session.Session{}, err
	}
	a.outputs.rememberInput(input.SessionID, text)
	if err := a.runPrompt(input.SessionID, text); err != nil {
		_, _ = a.registry.SetStatus(input.SessionID, session.Waiting, "")
		updated, _ := a.registry.SetLastMessage(input.SessionID, err.Error())
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
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if err := a.host.Health(ctx); err != nil {
		return mirror.Status{
			Enabled: false,
			Mode:    "host-offline",
			Address: hostclient.DefaultHTTPAddress,
			Note:    "agent-host offline. Rode: agent-host serve",
		}
	}
	return mirror.Status{
		Enabled: true,
		Mode:    "agent-host",
		Address: hostclient.DefaultHTTPAddress,
		Note:    "agent-host online.",
	}
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
	_, _ = a.registry.AppendTerminalOutput(sessionID, raw)
	current, ok := a.registry.Get(sessionID)
	if ok && current.Status == session.Busy {
		a.outputs.add(sessionID, raw, func(text string) {
			if text == "" {
				_, _ = a.registry.SetStatus(sessionID, session.Idle, "")
				a.emitState()
				return
			}
			if len(text) > 4000 {
				text = text[:4000] + "\n[saida truncada]"
			}
			_, _ = a.registry.AppendAssistant(sessionID, text)
			_, _ = a.registry.SetStatus(sessionID, session.Idle, "")
			a.emitState()
		})
		return
	}
	a.emitState()
}

func (a *App) handleTerminalExit(sessionID string, err error) {
	a.outputs.flush(sessionID, func(text string) {
		if text != "" {
			_, _ = a.registry.AppendAssistant(sessionID, text)
		}
	})
	_, _ = a.registry.SetTerminal(sessionID, false, 0)
	status := "Processo encerrado."
	if err != nil {
		status = "Processo encerrado: " + err.Error()
	}
	_, _ = a.registry.SetLastMessage(sessionID, status)
	_, _ = a.registry.SetStatus(sessionID, session.Offline, "")
	a.emitState()
}

func (a *App) subscribeHost(sessionID string) {
	go func() {
		err := a.host.Subscribe(context.Background(), sessionID, func(event hostclient.Event) {
			switch event.Type {
			case "output":
				a.handleTerminalOutput(sessionID, string(event.Data))
			case "exit":
				a.handleTerminalExit(sessionID, nil)
			}
		})
		if err != nil {
			_, _ = a.registry.SetTerminal(sessionID, false, 0)
			_, _ = a.registry.SetStatus(sessionID, session.Waiting, "")
			_, _ = a.registry.SetLastMessage(sessionID, "Conexao com agent-host perdida.")
			a.emitState()
		}
	}()
}

func (a *App) runPrompt(sessionID string, text string) error {
	item, ok := a.registry.Get(sessionID)
	if !ok {
		return errors.New("session not found")
	}
	provider, ok := a.providerByID(item.ProviderID)
	if !ok {
		return errors.New("provider not found")
	}

	if !item.TerminalAttached {
		pid, err := a.startTerminalSession(sessionID, provider, item.CWD)
		if err != nil {
			return errors.New("agent-host nao iniciou o terminal: " + err.Error())
		}
		_, _ = a.registry.SetTerminal(sessionID, true, pid)
		_, _ = a.registry.SetExternalAttach(sessionID, "agentctl attach "+sessionID)
		a.outputs.register(sessionID)
		a.subscribeHost(sessionID)
	}

	_, _ = a.registry.SetExternalAttach(sessionID, "agentctl attach "+sessionID)
	_, _ = a.registry.SetLastMessage(sessionID, "Executando no terminal local.")
	a.emitState()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.host.Send(ctx, sessionID, text); err != nil {
		if pid, startErr := a.startTerminalSession(sessionID, provider, item.CWD); startErr == nil {
			_, _ = a.registry.SetTerminal(sessionID, true, pid)
			_, _ = a.registry.SetExternalAttach(sessionID, "agentctl attach "+sessionID)
			a.outputs.register(sessionID)
			a.subscribeHost(sessionID)
			time.Sleep(200 * time.Millisecond)
			err = a.host.Send(ctx, sessionID, text)
		}
		if err != nil {
			return errors.New("agent-host nao enviou a mensagem ao terminal: " + err.Error())
		}
	}
	return nil
}

func (a *App) startTerminalSession(sessionID string, item provider.Provider, cwd string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, err := a.host.Start(ctx, hostclient.StartInput{
		SessionID: sessionID,
		Command:   item.Command,
		Args:      item.Args,
		CWD:       cwd,
	})
	if err != nil {
		return 0, err
	}
	return started.PID, nil
}

func cleanTerminalText(raw string) string {
	cleaned := ansiPattern.ReplaceAllString(raw, "")
	cleaned = strings.ReplaceAll(cleaned, "\x1b(B", "")
	cleaned = strings.ReplaceAll(cleaned, "\r\n", "\n")
	cleaned = strings.ReplaceAll(cleaned, "\r", "\n")
	cleaned = controlPattern.ReplaceAllString(cleaned, "")
	cleaned = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, cleaned)
	cleaned = strings.TrimSpace(cleaned)
	return cleaned
}

type outputFilter struct {
	mu      sync.Mutex
	buffers map[string]string
	inputs  map[string]string
	timers  map[string]*time.Timer
}

func newOutputFilter() *outputFilter {
	return &outputFilter{
		buffers: make(map[string]string),
		inputs:  make(map[string]string),
		timers:  make(map[string]*time.Timer),
	}
}

func (f *outputFilter) register(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buffers[sessionID] = ""
}

func (f *outputFilter) rememberInput(sessionID string, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs[sessionID] = strings.TrimSpace(text)
}

func (f *outputFilter) add(sessionID string, raw string, emit func(string)) {
	cleaned := cleanTerminalText(raw)
	if cleaned == "" {
		return
	}

	f.mu.Lock()
	f.buffers[sessionID] += "\n" + cleaned
	if timer := f.timers[sessionID]; timer != nil {
		timer.Stop()
	}
	f.timers[sessionID] = time.AfterFunc(2200*time.Millisecond, func() {
		f.flush(sessionID, emit)
	})
	f.mu.Unlock()
}

func (f *outputFilter) flush(sessionID string, emit func(string)) {
	f.mu.Lock()
	buffer := f.buffers[sessionID]
	f.buffers[sessionID] = ""
	lastInput := f.inputs[sessionID]
	if timer := f.timers[sessionID]; timer != nil {
		timer.Stop()
		delete(f.timers, sessionID)
	}
	f.mu.Unlock()

	text := filterAssistantText(buffer, lastInput)
	if text != "" {
		emit(text)
	}
}

func filterAssistantText(raw string, lastInput string) string {
	text := cleanTerminalText(raw)
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	lastInput = strings.TrimSpace(lastInput)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if shouldDropTerminalLine(line, lastInput) {
			continue
		}
		filtered = append(filtered, line)
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func shouldDropTerminalLine(line string, lastInput string) bool {
	if line == "" {
		return true
	}
	if lastInput != "" && strings.EqualFold(strings.TrimSpace(line), lastInput) {
		return true
	}
	if lastInput != "" {
		withoutPrompt := strings.TrimLeft(line, ">› $")
		if strings.EqualFold(strings.TrimSpace(withoutPrompt), lastInput) {
			return true
		}
	}

	trimmed := strings.Trim(line, " .-|+=_*")
	if trimmed == "" {
		return true
	}

	drops := []string{
		"? for shortcuts",
		"esc to interrupt",
		"ctrl+c",
		"ctrl+d",
		"press enter",
		"thinking",
		"tinkering",
		"cwd:",
		"welcome back",
		"what's new",
		"tips for getting started",
		"run /init",
		"release-notes",
		"mcp server failed",
		"running stop hooks",
		"tokens)",
		"claude code",
		"esc to interrupt",
	}
	lower := strings.ToLower(line)
	for _, drop := range drops {
		if strings.Contains(lower, drop) {
			return true
		}
	}
	return false
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
		Mirror:    a.MirrorStatus(),
	}
}

func (a *App) emitState() {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "state:update", a.bootstrap())
}
