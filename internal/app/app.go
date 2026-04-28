package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/provider"
	"agent-chat-local/internal/session"
	"agent-chat-local/internal/terminal"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)
var controlPattern = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

type App struct {
	ctx       context.Context
	registry  *session.Registry
	providers []provider.Provider
	mirror    *mirror.Server
	terminals *terminal.Manager
	outputs   *outputFilter
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
	terminals := terminal.NewManager()

	return &App{
		registry:  registry,
		providers: providers,
		mirror:    nil,
		terminals: terminals,
		outputs:   newOutputFilter(),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	if a.mirror == nil {
		a.mirror = mirror.NewServer(a.terminals, func(sessionID string, data []byte) {
			a.handleTerminalOutput(sessionID, string(data))
		})
	}
	_ = a.mirror.Start("127.0.0.1:47656")
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
	if runtime.GOOS == "darwin" {
		runCommand := "agentctl run " + created.ID + " " + shellJoin(append([]string{item.Command}, item.Args...))
		created, _ = a.registry.SetExternalAttach(created.ID, runCommand)
		created, _ = a.registry.SetLastMessage(created.ID, fmt.Sprintf("%s aguardando terminal externo seguro.", item.Name))
		a.emitState()
		return created, nil
	}

	process, err := a.terminals.Start(terminal.StartOptions{
		SessionID: created.ID,
		Command:   item.Command,
		Args:      item.Args,
		CWD:       created.CWD,
		OnOutput:  a.handleTerminalOutput,
		OnExit:    a.handleTerminalExit,
	})
	if err != nil {
		_, _ = a.registry.SetStatus(created.ID, session.Offline, "")
		updated, _ := a.registry.SetLastMessage(created.ID, "Falha ao iniciar "+item.CLI)
		a.emitState()
		return updated, err
	}

	created, _ = a.registry.SetTerminal(created.ID, true, process.PID())
	a.outputs.register(created.ID)
	created, _ = a.registry.SetLastMessage(created.ID, fmt.Sprintf("%s conectado em terminal oculto.", item.Name))
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
	a.outputs.rememberInput(input.SessionID, text)
	if err := a.terminals.SendLine(input.SessionID, text); err != nil {
		if a.mirror != nil && a.mirror.Send(input.SessionID, []byte(text+"\r")) {
			updated, _ := a.registry.Get(input.SessionID)
			a.emitState()
			return updated, nil
		}
		_, _ = a.registry.SetStatus(input.SessionID, session.Waiting, "")
		updated, _ := a.registry.SetLastMessage(input.SessionID, "Abra o terminal externo para conectar este chat.")
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
	if a.mirror == nil {
		return mirror.Status{}
	}
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
	a.outputs.add(sessionID, raw, func(text string) {
		if text == "" {
			return
		}
		if len(text) > 4000 {
			text = text[:4000] + "\n[saida truncada]"
		}
		_, _ = a.registry.AppendAssistant(sessionID, text)
		_, _ = a.registry.SetStatus(sessionID, session.Idle, "")
		a.emitState()
	})
}

func (a *App) handleTerminalExit(sessionID string, err error) {
	a.terminals.Forget(sessionID)
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

func shellJoin(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if strings.ContainsAny(part, " \t\n'\"\\$`") {
			quoted = append(quoted, "'"+strings.ReplaceAll(part, "'", "'\\''")+"'")
			continue
		}
		quoted = append(quoted, part)
	}
	return strings.Join(quoted, " ")
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
	f.timers[sessionID] = time.AfterFunc(450*time.Millisecond, func() {
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
		"cwd:",
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
