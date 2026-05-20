package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/luisdemarchi/CLIchat/internal/agent"
	"github.com/luisdemarchi/CLIchat/internal/hostclient"
	"github.com/luisdemarchi/CLIchat/internal/mirror"
	"github.com/luisdemarchi/CLIchat/internal/provider"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx       context.Context
	host      *hostclient.Client
	providers []provider.Provider

	mu        sync.RWMutex
	instances map[string]agent.Instance
	selected  string

	subscribed map[string]bool
	subMu      sync.Mutex

	stateCancel context.CancelFunc
}

type Provider = provider.Provider

type Session struct {
	ID               string                `json:"id"`
	Title            string                `json:"title"`
	Topic            string                `json:"topic,omitempty"`
	ProviderID       string                `json:"providerId"`
	ProviderTag      string                `json:"providerTag"`
	ProviderAccent   string                `json:"providerAccent"`
	Origin           agent.Origin          `json:"origin"`
	Status           agent.Status          `json:"status"`
	CWD              string                `json:"cwd,omitempty"`
	AvatarLabel      string                `json:"avatarLabel"`
	LastMessage      string                `json:"lastMessage"`
	CurrentTool      string                `json:"currentTool,omitempty"`
	ProcessID        int                   `json:"processId,omitempty"`
	TTY              string                `json:"tty,omitempty"`
	ClaudeSessionID  string                `json:"claudeSessionId,omitempty"`
	ExternalAttach   string                `json:"externalAttach"`
	CreatedAt        string                `json:"createdAt"`
	UpdatedAt        string                `json:"updatedAt"`
	Messages         []agent.Message       `json:"messages"`
	MessageCount     int                   `json:"messageCount"`
	PendingQuestion  string                `json:"pendingQuestion,omitempty"`
	PendingActions   []agent.PendingAction `json:"pendingActions"`
	TerminalAttached bool                  `json:"terminalAttached"`
	TranscriptPath   string                `json:"transcriptPath,omitempty"`
}

type Bootstrap struct {
	Providers []provider.Provider `json:"providers"`
	Sessions  []Session           `json:"sessions"`
	Selected  *Session            `json:"selected,omitempty"`
	Mirror    mirror.Status       `json:"mirror"`
}

type CreateInput struct {
	ProviderID provider.ID `json:"providerId"`
	Title      string      `json:"title"`
	CWD        string      `json:"cwd"`
}

type SendInput struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

type TerminalInput struct {
	SessionID string `json:"sessionId"`
}

type OpenSessionTerminalInput struct {
	SessionID  string `json:"sessionId"`
	ProviderID string `json:"providerId,omitempty"`
}

type TerminalActionInput struct {
	SessionID string `json:"sessionId"`
	ActionID  string `json:"actionId"`
	Input     string `json:"input"`
}

type TerminalRawInput struct {
	SessionID string `json:"sessionId"`
	Data      string `json:"data"`
}

type SendFilesInput struct {
	SessionID string   `json:"sessionId"`
	Paths     []string `json:"paths"`
}

func New() *App {
	return &App{
		host:       hostclient.New(""),
		providers:  provider.Defaults(),
		instances:  make(map[string]agent.Instance),
		subscribed: make(map[string]bool),
	}
}

func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	go a.bootstrapLoop()
}

func (a *App) bootstrapLoop() {
	for {
		if a.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		err := a.host.Health(ctx)
		cancel()
		if err == nil {
			break
		}
		select {
		case <-time.After(1500 * time.Millisecond):
		case <-a.ctx.Done():
			return
		}
	}

	if state, err := a.host.State(context.Background()); err == nil {
		a.mu.Lock()
		for _, inst := range state.Instances {
			a.instances[inst.ID] = inst
		}
		a.mu.Unlock()
		a.subscribeInternalSessions()
	}

	a.emitState()
	a.startStateStream()
}

func (a *App) startStateStream() {
	if a.stateCancel != nil {
		a.stateCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.stateCancel = cancel

	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			err := a.host.SubscribeState(ctx, func(event hostclient.StateEvent) {
				switch event.Kind {
				case "snapshot":
					var payload struct {
						Instances []agent.Instance `json:"instances"`
					}
					if err := json.Unmarshal(event.Payload, &payload); err == nil {
						a.applySnapshot(payload.Instances)
					}
				case "instance_updated":
					var payload struct {
						Payload agent.Instance `json:"payload"`
					}
					if err := json.Unmarshal(event.Payload, &payload); err == nil {
						a.applyInstance(payload.Payload)
					}
				case "instance_removed":
					var payload struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(event.Payload, &payload); err == nil {
						a.removeInstance(payload.ID)
					}
				}
			})
			if ctx.Err() != nil {
				return
			}
			delay := 500 * time.Millisecond
			if err != nil {
				delay = 2 * time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

func (a *App) applySnapshot(list []agent.Instance) {
	a.mu.Lock()
	a.instances = make(map[string]agent.Instance, len(list))
	for _, inst := range list {
		a.instances[inst.ID] = inst
	}
	a.mu.Unlock()
	a.subscribeInternalSessions()
	a.emitState()
}

func (a *App) applyInstance(inst agent.Instance) {
	a.mu.Lock()
	a.instances[inst.ID] = inst
	a.mu.Unlock()
	if inst.Origin == agent.OriginInternal && inst.TerminalAttached {
		a.subscribeOutput(inst.ID)
	}
	a.emitState()
}

func (a *App) removeInstance(id string) {
	a.mu.Lock()
	delete(a.instances, id)
	if a.selected == id {
		a.selected = ""
	}
	a.mu.Unlock()
	a.subMu.Lock()
	delete(a.subscribed, id)
	a.subMu.Unlock()
	a.emitState()
}

func (a *App) subscribeInternalSessions() {
	a.mu.RLock()
	ids := make([]string, 0, len(a.instances))
	for id, inst := range a.instances {
		if inst.Origin == agent.OriginInternal && inst.TerminalAttached {
			ids = append(ids, id)
		}
	}
	a.mu.RUnlock()
	for _, id := range ids {
		a.subscribeOutput(id)
	}
}

func (a *App) subscribeOutput(id string) {
	a.subMu.Lock()
	if a.subscribed[id] {
		a.subMu.Unlock()
		return
	}
	a.subscribed[id] = true
	a.subMu.Unlock()

	go func() {
		err := a.host.SubscribeOutput(context.Background(), id, func(event hostclient.Event) {
			switch event.Type {
			case "output":
				if a.ctx != nil && len(event.Data) > 0 {
					wailsruntime.EventsEmit(a.ctx, "terminal:"+id, base64.StdEncoding.EncodeToString(event.Data))
				}
			case "exit":
				a.subMu.Lock()
				delete(a.subscribed, id)
				a.subMu.Unlock()
				if a.ctx != nil {
					wailsruntime.EventsEmit(a.ctx, "terminal:"+id+":exit", true)
				}
			}
		})
		_ = err
		a.subMu.Lock()
		delete(a.subscribed, id)
		a.subMu.Unlock()
	}()
}

func (a *App) forgetOutputSubscription(id string) {
	a.subMu.Lock()
	delete(a.subscribed, id)
	a.subMu.Unlock()
}

// ---------------------------------------------------------------------------
// Wails-bound API
// ---------------------------------------------------------------------------

func (a *App) GetBootstrap() Bootstrap {
	return a.bootstrap()
}

func (a *App) ListProviders() []provider.Provider {
	return a.providers
}

func (a *App) ListSessions() []Session {
	return a.sessions("")
}

// ReconnectSession is kept for older frontends. It now opens a fresh terminal
// with the chat handoff instead of resuming a provider-specific global session.
func (a *App) ReconnectSession(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("session id is required")
	}
	_, err := a.OpenSessionTerminal(OpenSessionTerminalInput{SessionID: id})
	return err
}

func (a *App) DeleteSession(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("session id is required")
	}
	a.mu.RLock()
	_, ok := a.instances[id]
	a.mu.RUnlock()
	if !ok {
		return errors.New("session not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.host.DeleteInstance(ctx, id); err != nil {
		return err
	}
	a.removeInstance(id)
	return nil
}

func (a *App) SelectSession(id string) (Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	inst, ok := a.instances[id]
	if !ok {
		return Session{}, errors.New("session not found")
	}
	a.selected = id
	return a.toSession(inst), nil
}

func (a *App) CreateChat(input CreateInput) (Session, error) {
	prov, ok := a.providerByID(provider.ID(input.ProviderID))
	if !ok {
		return Session{}, fmt.Errorf("unknown provider: %s", input.ProviderID)
	}
	if !prov.Available {
		return Session{}, fmt.Errorf("%s CLI not found", prov.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.host.Health(ctx); err != nil {
		return Session{}, errors.New("clichat-host is offline. Run: clichat-host serve")
	}

	inst, err := a.host.CreateInstance(ctx, hostclient.CreateInstanceInput{
		Origin:     agent.OriginInternal,
		ProviderID: string(prov.ID),
		Title:      input.Title,
		CWD:        input.CWD,
	})
	if err != nil {
		return Session{}, err
	}

	startCWD := sessionCWD(inst.ID, input.CWD)
	args := append([]string{}, prov.Args...)
	if prov.ID == provider.Claude {
		args = append(args, claudeAgentChatArgs(inst.ID, prov)...)
	}
	if prov.ID == provider.Gemini {
		args = geminiArgs(inst, args, false)
	}
	if prov.ID == provider.Codex {
		_ = ensureCodexTrusted(startCWD)
	}
	env := []string{
		"AGENT_CHAT_INTERNAL_SESSION_ID=" + inst.ID,
		"AGENT_CHAT_HOST=" + a.host.BaseURL(),
	}
	updated, err := a.host.StartTerminal(ctx, inst.ID, hostclient.StartTerminalInput{
		Command: prov.Command,
		Args:    args,
		CWD:     startCWD,
		Env:     env,
	})
	if err != nil {
		return a.toSession(inst), err
	}

	a.mu.Lock()
	a.instances[updated.ID] = updated
	a.selected = updated.ID
	a.mu.Unlock()
	a.subscribeOutput(updated.ID)
	a.emitState()
	return a.toSession(updated), nil
}

func (a *App) OpenSessionTerminal(input OpenSessionTerminalInput) (Session, error) {
	id := strings.TrimSpace(input.SessionID)
	if id == "" {
		return Session{}, errors.New("session id is required")
	}

	a.mu.RLock()
	inst, ok := a.instances[id]
	a.mu.RUnlock()
	if !ok {
		return Session{}, errors.New("session not found")
	}
	if inst.Origin != agent.OriginInternal {
		return a.toSession(inst), errors.New("external sessions are read-only")
	}

	providerID := strings.TrimSpace(input.ProviderID)
	if providerID == "" {
		providerID = inst.ProviderID
	}
	prov, ok := a.providerByID(provider.ID(providerID))
	if !ok {
		return a.toSession(inst), fmt.Errorf("unknown provider: %s", providerID)
	}
	if !prov.Available {
		return a.toSession(inst), fmt.Errorf("%s CLI not found", prov.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := a.host.Health(ctx); err != nil {
		return a.toSession(inst), errors.New("clichat-host is offline. Run: clichat-host serve")
	}

	_, _ = a.host.StopTerminal(ctx, inst.ID)
	a.forgetOutputSubscription(inst.ID)

	if inst.ProviderID != providerID {
		updated, err := a.host.SetProvider(ctx, inst.ID, providerID)
		if err != nil {
			return a.toSession(inst), err
		}
		inst = updated
	}
	memory, _ := a.host.ConversationMemory(ctx, inst.ID)

	startCWD := sessionCWD(inst.ID, inst.CWD)
	args := freshProviderArgs(inst, prov)
	if prov.ID == provider.Codex {
		_ = ensureCodexTrusted(startCWD)
	}
	env := []string{
		"AGENT_CHAT_INTERNAL_SESSION_ID=" + inst.ID,
		"AGENT_CHAT_HOST=" + a.host.BaseURL(),
	}
	started, err := a.host.StartTerminal(ctx, inst.ID, hostclient.StartTerminalInput{
		Command: prov.Command,
		Args:    args,
		CWD:     startCWD,
		Env:     env,
	})
	if err != nil {
		return a.toSession(inst), err
	}

	a.mu.Lock()
	a.instances[started.ID] = started
	a.selected = started.ID
	a.mu.Unlock()
	a.subscribeOutput(started.ID)

	handoff := conversationHandoffPrompt(started, prov, memory)
	if handoff != "" {
		_, _ = a.host.AppendMessage(ctx, started.ID, agent.RoleSystem,
			fmt.Sprintf("Terminal %s started with the chat memory.", prov.Name))
		_, _ = a.host.SetStatus(ctx, started.ID, agent.StatusBusy, "")
		if err := a.host.SendText(ctx, started.ID, handoff); err != nil {
			_, _ = a.host.SetStatus(ctx, started.ID, agent.StatusWaiting, "")
			return a.toSession(started), err
		}
	}

	if fresh, err := a.host.GetInstance(ctx, started.ID); err == nil {
		a.mu.Lock()
		a.instances[fresh.ID] = fresh
		a.mu.Unlock()
		a.emitState()
		return a.toSession(fresh), nil
	}
	a.emitState()
	return a.toSession(started), nil
}

// FocusTerminal brings the OS terminal window/tab that hosts this session to the front.
// Best-effort: works on macOS Terminal.app via AppleScript; other platforms just activate
// the terminal app.
func (a *App) FocusTerminal(sessionID string) error {
	a.mu.RLock()
	inst, ok := a.instances[sessionID]
	a.mu.RUnlock()
	if !ok {
		return errors.New("session not found")
	}
	return focusTerminalForSession(inst.TTY, inst.PID)
}

func (a *App) SendMessage(input SendInput) (Session, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return Session{}, errors.New("message cannot be empty")
	}

	a.mu.RLock()
	inst, ok := a.instances[input.SessionID]
	a.mu.RUnlock()
	if !ok {
		return Session{}, errors.New("session not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updated, err := a.host.AppendMessage(ctx, inst.ID, agent.RoleUser, text)
	if err != nil {
		return a.toSession(inst), err
	}
	a.applyInstance(updated)
	busy, err := a.host.SetStatus(ctx, inst.ID, agent.StatusBusy, "")
	if err != nil {
		return a.toSession(updated), err
	}
	a.applyInstance(busy)

	if inst.Origin == agent.OriginInternal {
		if err := a.host.SendText(ctx, inst.ID, text); err != nil {
			waiting, _ := a.host.SetStatus(ctx, inst.ID, agent.StatusWaiting, "")
			if waiting.ID != "" {
				a.applyInstance(waiting)
				return a.toSession(waiting), err
			}
			return a.toSession(busy), err
		}
	} else {
		if err := sendToExternalTerminal(inst.TTY, text); err != nil {
			waiting, _ := a.host.SetStatus(ctx, inst.ID, agent.StatusWaiting, "")
			if waiting.ID != "" {
				a.applyInstance(waiting)
				return a.toSession(waiting), err
			}
			return a.toSession(busy), err
		}
	}
	return a.toSession(busy), nil
}

// PickFiles opens a native multi-file picker and returns the selected absolute
// paths. Empty slice when the user cancels.
func (a *App) PickFiles() ([]string, error) {
	if a.ctx == nil {
		return nil, errors.New("wails context unavailable")
	}
	paths, err := wailsruntime.OpenMultipleFilesDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Attach files",
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// SendFiles delivers each file path to the underlying CLI as a separate input
// line, in sequence (the TUI processes them one at a time). A single user
// bubble groups all attached paths so the chat stays clean.
func (a *App) SendFiles(input SendFilesInput) (Session, error) {
	paths := make([]string, 0, len(input.Paths))
	for _, p := range input.Paths {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	if len(paths) == 0 {
		return Session{}, errors.New("no files selected")
	}

	a.mu.RLock()
	inst, ok := a.instances[input.SessionID]
	a.mu.RUnlock()
	if !ok {
		return Session{}, errors.New("session not found")
	}
	if inst.Origin != agent.OriginInternal {
		return a.toSession(inst), errors.New("file attachment only works for internal sessions")
	}

	bubble := "Attaching " + pluralFiles(len(paths)) + ":\n"
	for _, p := range paths {
		bubble += "- " + p + "\n"
	}
	bubble = strings.TrimRight(bubble, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	updated, err := a.host.AppendMessage(ctx, inst.ID, agent.RoleUser, bubble)
	if err != nil {
		return a.toSession(inst), err
	}
	a.applyInstance(updated)
	busy, err := a.host.SetStatus(ctx, inst.ID, agent.StatusBusy, "")
	if err != nil {
		return a.toSession(updated), err
	}
	a.applyInstance(busy)

	for i, p := range paths {
		if err := a.host.SendText(ctx, inst.ID, "@"+p); err != nil {
			waiting, _ := a.host.SetStatus(ctx, inst.ID, agent.StatusWaiting, "")
			if waiting.ID != "" {
				a.applyInstance(waiting)
				return a.toSession(waiting), err
			}
			return a.toSession(busy), err
		}
		// give the TUI a moment to settle before pushing the next path
		if i < len(paths)-1 {
			time.Sleep(350 * time.Millisecond)
		}
	}
	return a.toSession(busy), nil
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func (a *App) RespondToPrompt(input TerminalActionInput) (Session, error) {
	a.mu.RLock()
	inst, ok := a.instances[input.SessionID]
	a.mu.RUnlock()
	if !ok {
		return Session{}, errors.New("session not found")
	}
	if len(inst.PendingActions) == 0 {
		return a.toSession(inst), errors.New("no pending action")
	}
	value := input.Input
	for _, action := range inst.PendingActions {
		if action.ID == input.ActionID {
			value = action.Input
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if inst.Origin == agent.OriginInternal {
		if err := a.host.SendText(ctx, inst.ID, value); err != nil {
			return a.toSession(inst), err
		}
	}
	updated, _ := a.host.ClearPending(ctx, inst.ID)
	if updated.ID != "" {
		a.applyInstance(updated)
	}
	idle, _ := a.host.SetStatus(ctx, inst.ID, agent.StatusIdle, "")
	if idle.ID != "" {
		a.applyInstance(idle)
		return a.toSession(idle), nil
	}
	return a.toSession(updated), nil
}

type TerminalResizeInput struct {
	SessionID string `json:"sessionId"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

func (a *App) ResizeTerminal(input TerminalResizeInput) error {
	if strings.TrimSpace(input.SessionID) == "" {
		return errors.New("session id is required")
	}
	if input.Cols == 0 || input.Rows == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return a.host.ResizeTerminal(ctx, input.SessionID, input.Cols, input.Rows)
}

func (a *App) SendTerminalInput(input TerminalRawInput) error {
	if strings.TrimSpace(input.SessionID) == "" {
		return errors.New("session id is required")
	}
	if input.Data == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.host.SendData(ctx, input.SessionID, []byte(input.Data))
}

func (a *App) OpenTerminal(input TerminalInput) (string, error) {
	a.mu.RLock()
	inst, ok := a.instances[input.SessionID]
	a.mu.RUnlock()
	if !ok {
		return "", errors.New("session not found")
	}
	command := externalAttachCommand(inst)
	if command == "" {
		return "", errors.New("attach command unavailable")
	}
	if a.ctx != nil {
		wailsruntime.ClipboardSetText(a.ctx, command)
	}
	_ = openExternalTerminal(command)
	return command, nil
}

func (a *App) ExternalAttachCommand(sessionID string) (string, error) {
	a.mu.RLock()
	inst, ok := a.instances[sessionID]
	a.mu.RUnlock()
	if !ok {
		return "", errors.New("session not found")
	}
	return externalAttachCommand(inst), nil
}

func (a *App) MirrorStatus() mirror.Status {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	if err := a.host.Health(ctx); err != nil {
		return mirror.Status{
			Enabled: false,
			Mode:    "host-offline",
			Address: hostclient.DefaultHTTPAddress,
			Note:    "clichat-host is offline. Run: clichat-host serve",
		}
	}
	return mirror.Status{
		Enabled: true,
		Mode:    "clichat-host",
		Address: hostclient.DefaultHTTPAddress,
		Note:    "clichat-host online.",
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (a *App) bootstrap() Bootstrap {
	selectedID := a.selected
	sessions := a.sessions(selectedID)
	var selected *Session
	if selectedID != "" {
		for i := range sessions {
			if sessions[i].ID == selectedID {
				copy := sessions[i]
				selected = &copy
				break
			}
		}
	}
	if selected == nil && len(sessions) > 0 {
		selectedID = sessions[0].ID
		sessions = a.sessions(selectedID)
		copy := sessions[0]
		selected = &copy
	}
	return Bootstrap{
		Providers: a.providers,
		Sessions:  sessions,
		Selected:  selected,
		Mirror:    a.MirrorStatus(),
	}
}

func (a *App) sessions(includeMessagesFor string) []Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Session, 0, len(a.instances))
	for _, inst := range a.instances {
		// Hide external sessions from the UI: end-users should see only chats this app
		// owns, where it can drive the PTY directly without OS permission prompts.
		if inst.Origin == agent.OriginExternal {
			continue
		}
		out = append(out, a.toSessionWithMessages(inst, inst.ID == includeMessagesFor))
	}
	// newest updated first
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAt > out[i].UpdatedAt {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (a *App) toSession(inst agent.Instance) Session {
	return a.toSessionWithMessages(inst, true)
}

func (a *App) toSessionWithMessages(inst agent.Instance, includeMessages bool) Session {
	prov, _ := a.providerByID(provider.ID(inst.ProviderID))
	avatar := avatarLabel(inst.Title, prov.Name)
	tag := prov.Tag
	if tag == "" {
		tag = strings.ToUpper(inst.ProviderID)
	}
	accent := prov.Accent
	if accent == "" {
		accent = "#5b6675"
	}
	external := externalAttachCommand(inst)
	last := inst.LastMessage
	if last == "" && len(inst.Messages) > 0 {
		last = inst.Messages[len(inst.Messages)-1].Text
	}
	if last == "" && inst.Origin == agent.OriginExternal {
		last = "External session detected. Waiting for the first reply."
	}
	messages := []agent.Message{}
	if includeMessages {
		messages = append(messages, inst.Messages...)
	}

	return Session{
		ID:               inst.ID,
		Title:            inst.Title,
		Topic:            inst.Topic,
		ProviderID:       inst.ProviderID,
		ProviderTag:      tag,
		ProviderAccent:   accent,
		Origin:           inst.Origin,
		Status:           inst.Status,
		CWD:              inst.CWD,
		AvatarLabel:      avatar,
		LastMessage:      last,
		CurrentTool:      inst.CurrentTool,
		ProcessID:        inst.PID,
		TTY:              inst.TTY,
		ClaudeSessionID:  inst.ClaudeSessionID,
		ExternalAttach:   external,
		CreatedAt:        inst.CreatedAt,
		UpdatedAt:        inst.UpdatedAt,
		Messages:         messages,
		MessageCount:     len(inst.Messages),
		PendingQuestion:  inst.PendingQuestion,
		PendingActions:   append([]agent.PendingAction(nil), inst.PendingActions...),
		TerminalAttached: inst.TerminalAttached,
		TranscriptPath:   inst.TranscriptPath,
	}
}

func (a *App) emitState() {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "state:update", a.bootstrap())
}

func (a *App) providerByID(id provider.ID) (provider.Provider, bool) {
	for _, item := range a.providers {
		if item.ID == id {
			return item, true
		}
	}
	return provider.Provider{}, false
}

// ---------------------------------------------------------------------------
// Provider-specific args / external attach
// ---------------------------------------------------------------------------

func claudeAgentChatArgs(sessionID string, _ provider.Provider) []string {
	// MCP server bridging back to clichat-host. We only expose the topic-update
	// tool — chat bubbles still come from the JSONL transcript watcher (more
	// reliable than depending on Claude calling agent_chat_reply on every turn).
	mcpConfig := `{"mcpServers":{"clichat":{"type":"http","url":"http://127.0.0.1:47657/mcp"}}}`
	prompt := strings.Join([]string{
		"You are running inside CLIchat.",
		"At the start of every new task — and whenever the focus changes — call the MCP tool agent_chat_set_topic with session_id=" + sessionID + " and a short 2-6 word topic in the user's language describing what you are doing right now.",
		"Examples: 'analisando card S3-15693', 'corrigindo bug auth', 'revisando PR #42'.",
		"Keep calling agent_chat_set_topic as the work shifts so the chat list always shows the current task.",
	}, " ")
	return []string{
		"--mcp-config", mcpConfig,
		"--allowedTools", "mcp__clichat__agent_chat_set_topic",
		"--append-system-prompt", prompt,
	}
}

func freshProviderArgs(inst agent.Instance, prov provider.Provider) []string {
	args := append([]string{}, prov.Args...)
	switch prov.ID {
	case provider.Claude:
		args = append(args, claudeAgentChatArgs(inst.ID, prov)...)
	case provider.Gemini:
		args = geminiArgs(inst, args, false)
	}
	return args
}

func sessionCWD(sessionID string, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	home, err := os.UserHomeDir()
	if err != nil {
		return cwd
	}
	if cwd == "" || cwd == home {
		return filepath.Join(home, ".clichat", "sandbox", sessionID)
	}
	return cwd
}

func ensureCodexTrusted(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".codex", "config.toml")
	section := `[projects.` + tomlString(cwd) + `]`
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), section) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	if len(data) > 0 {
		b.WriteString(bytesTrimRightNewlines(data))
		b.WriteString("\n\n")
	}
	b.WriteString(section)
	b.WriteString("\ntrust_level = \"trusted\"\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func bytesTrimRightNewlines(data []byte) string {
	return strings.TrimRight(string(data), "\r\n")
}

func conversationHandoffPrompt(inst agent.Instance, prov provider.Provider, memory hostclient.ConversationMemory) string {
	summary := strings.TrimSpace(memory.Summary)
	if summary == "" {
		messages := nonSystemMessages(inst.Messages)
		if len(messages) == 0 {
			return ""
		}
		summary = summarizeMessages(messages, 12000)
	}
	if strings.TrimSpace(summary) == "" {
		return ""
	}
	topic := strings.TrimSpace(firstNonEmpty(memory.Topic, inst.Topic, inst.Title))
	var b strings.Builder
	b.WriteString("<clichat-handoff>\n")
	b.WriteString("You are taking over an existing CLIchat conversation.\n")
	b.WriteString("This is the same chat; only the terminal/agent changed to ")
	b.WriteString(prov.Name)
	b.WriteString(".\n\n")
	if topic != "" {
		b.WriteString("Current chat topic: ")
		b.WriteString(topic)
		b.WriteString(".\n\n")
	}
	b.WriteString("Use the summary below as startup context. Do not repeat the summary to the user. ")
	b.WriteString("Reply only with a short sentence saying you have taken over the conversation and wait for the next instruction, unless there is a clear pending request.\n\n")
	b.WriteString("Internal memory for this conversation:\n")
	b.WriteString(summary)
	b.WriteString("\n</clichat-handoff>")
	return b.String()
}

func nonSystemMessages(messages []agent.Message) []agent.Message {
	out := make([]agent.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == agent.RoleSystem {
			continue
		}
		if strings.TrimSpace(msg.Text) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func summarizeMessages(messages []agent.Message, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 12000
	}
	var lines []string
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		text := compactForHandoff(msg.Text)
		if text == "" {
			continue
		}
		line := fmt.Sprintf("- %s: %s", roleLabel(msg.Role), text)
		if total+len(line) > maxChars {
			break
		}
		lines = append([]string{line}, lines...)
		total += len(line)
	}
	if len(lines) == 0 {
		return "- No usable recent messages."
	}
	return strings.Join(lines, "\n")
}

func compactForHandoff(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 900 {
		text = text[:900] + "..."
	}
	return text
}

func roleLabel(role agent.Role) string {
	switch role {
	case agent.RoleUser:
		return "User"
	case agent.RoleAssistant:
		return "Assistant"
	default:
		return "System"
	}
}

func geminiArgs(inst agent.Instance, base []string, resume bool) []string {
	sessionID := firstNonEmpty(inst.ProviderSessionID, instanceUUID(inst.ID))
	if sessionID == "" {
		return base
	}
	args := append([]string{}, base...)
	if resume {
		return append(args, "--resume", sessionID)
	}
	return append(args, "--session-id", sessionID)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func externalAttachCommand(inst agent.Instance) string {
	if inst.Origin == agent.OriginInternal {
		return "clichat attach " + inst.ID
	}
	if inst.PID != 0 {
		return fmt.Sprintf("ps -p %d", inst.PID)
	}
	return ""
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

func openExternalTerminal(command string) error {
	switch runtime.GOOS {
	case "darwin":
		return openMacTerminal(command)
	case "linux":
		if path, err := exec.LookPath("x-terminal-emulator"); err == nil {
			return exec.Command(path, "-e", command).Start()
		}
		for _, name := range []string{"gnome-terminal", "konsole", "xfce4-terminal", "xterm"} {
			if path, err := exec.LookPath(name); err == nil {
				return exec.Command(path, "-e", command).Start()
			}
		}
		return errors.New("nenhum terminal grafico encontrado")
	case "windows":
		return exec.Command("cmd", "/c", "start", "cmd", "/k", command).Start()
	default:
		return errors.New("opening an external terminal is not supported on this system")
	}
}

// openTerminalRunning opens a fresh OS-native terminal window and runs `command`
// inside it (typically `claude`). On macOS uses Terminal.app via AppleScript so
// the window stays open after Claude exits.
func openTerminalRunning(command string, cwd string) error {
	if command == "" {
		return errors.New("empty command")
	}
	full := command
	if strings.TrimSpace(cwd) != "" {
		full = "cd " + shellQuote(cwd) + " && " + command
	}
	switch runtime.GOOS {
	case "darwin":
		// Open without `activate` and immediately miniaturize so the new window
		// does not steal focus. User can bring it back via the Focus button.
		script := `tell application "Terminal"
	do script "` + escapeAppleScript(full) + `"
	delay 0.1
	try
		set miniaturized of front window to true
	end try
end tell`
		return exec.Command("osascript", "-e", script).Run()
	default:
		return openExternalTerminal(full)
	}
}

// focusTerminalForSession brings the OS terminal window/tab serving this session to the front.
func focusTerminalForSession(tty string, pid int) error {
	switch runtime.GOOS {
	case "darwin":
		if strings.HasPrefix(tty, "/dev/") {
			tty = strings.TrimPrefix(tty, "/dev/")
		}
		var script string
		if tty != "" {
			script = `tell application "Terminal"
	activate
	repeat with w in windows
		repeat with t in tabs of w
			try
				if (tty of t) ends with "` + escapeAppleScript(tty) + `" then
					if miniaturized of w then set miniaturized of w to false
					set selected of t to true
					set frontmost of w to true
					set index of w to 1
					return
				end if
			end try
		end repeat
	end repeat
end tell`
		} else {
			script = `tell application "Terminal" to activate`
		}
		return exec.Command("osascript", "-e", script).Run()
	case "linux", "windows":
		return errors.New("focusing an external terminal is not supported on this system yet")
	default:
		return errors.New("unsupported platform")
	}
}

func escapeAppleScript(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "\"", "\\\"")
	return text
}

// sendToExternalTerminal injects `text` (plus a return) into the OS terminal that
// hosts this session. Preferred path is TIOCSTI on the pty — silent, no focus,
// no window pop. Falls back to AppleScript keystrokes if TIOCSTI is unavailable
// (e.g. SIP/sandbox), in which case the Terminal window may steal focus once.
func sendToExternalTerminal(tty string, text string) error {
	if strings.TrimSpace(tty) == "" {
		return errors.New("session has no known tty; open or focus the terminal first")
	}
	if err := injectIntoTTY(tty, text); err == nil {
		return nil
	} else {
		// fall through to AppleScript
		_ = err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("sending to an external terminal is not supported on this system")
	}

	short := strings.TrimPrefix(tty, "/dev/")
	var script strings.Builder
	script.WriteString(`tell application "Terminal"
	repeat with w in windows
		repeat with t in tabs of w
			try
				if (tty of t) ends with "` + escapeAppleScript(short) + `" then
					set selected of t to true
					set frontmost of w to true
				end if
			end try
		end repeat
	end repeat
end tell
tell application "System Events"
	set frontmost of process "Terminal" to true
`)
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			script.WriteString(`	keystroke "` + escapeAppleScript(line) + `"` + "\n")
		}
		script.WriteString("\tkeystroke return\n")
	}
	script.WriteString("end tell\n")

	return exec.Command("osascript", "-e", script.String()).Run()
}

func openMacTerminal(command string) error {
	clichat := "clichat"
	if path, err := exec.LookPath("clichat"); err == nil {
		clichat = path
	} else if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", "clichat")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			clichat = candidate
		}
	}
	parts := strings.Fields(command)
	if len(parts) >= 3 && parts[0] == "clichat" && parts[1] == "attach" {
		command = shellQuote(clichat) + " attach " + shellQuote(parts[2])
	}

	file, err := os.CreateTemp("", "agent-chat-*.command")
	if err != nil {
		return err
	}
	path := file.Name()
	content := "#!/bin/zsh\nclear\n" + command + "\n"
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return exec.Command("open", path).Start()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
