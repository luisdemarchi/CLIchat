package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luisdemarchi/CLIchat/internal/agent"
	"github.com/luisdemarchi/CLIchat/internal/memory"
	"github.com/luisdemarchi/CLIchat/internal/mirror"
	"github.com/luisdemarchi/CLIchat/internal/terminal"
)

const (
	defaultHTTPAddress = "127.0.0.1:47657"
	defaultTTYAddress  = "127.0.0.1:47656"
)

type server struct {
	manager   *terminal.Manager
	memory    *memory.Store
	mirror    *mirror.Server
	store     *agent.Store
	watcher   *agent.TranscriptWatcher
	prompts   *agent.PromptDetector
	eventsMu  sync.Mutex
	nextSubID int
	eventSubs map[string]map[int]chan hostEvent
}

type hostEvent struct {
	Type string
	Data []byte
}

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: clichat-host serve [--http addr] [--attach addr] [--state path] [--memory path]")
	}

	if len(os.Args) < 2 || os.Args[1] != "serve" {
		flag.Usage()
		os.Exit(2)
	}

	args := flag.NewFlagSet("serve", flag.ExitOnError)
	httpAddr := args.String("http", defaultHTTPAddress, "HTTP listen address")
	attachAddr := args.String("attach", defaultTTYAddress, "TTY attach address")
	statePath := args.String("state", defaultStatePath(), "Path to persistent state file")
	memoryPath := args.String("memory", defaultMemoryPath(), "Path to SQLite memory database")
	if err := args.Parse(os.Args[2:]); err != nil {
		log.Fatal(err)
	}

	store, err := agent.NewStore(*statePath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	memoryStore, err := memory.New(*memoryPath)
	if err != nil {
		log.Fatalf("memory: %v", err)
	}

	manager := terminal.NewManager()
	s := &server{
		manager:   manager,
		memory:    memoryStore,
		store:     store,
		prompts:   agent.NewPromptDetector(),
		eventSubs: make(map[string]map[int]chan hostEvent),
	}
	store.Subscribe(s.syncMemoryEvents)
	s.watcher = agent.NewTranscriptWatcher(store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.watcher.Start(ctx)

	s.mirror = mirror.NewServer(manager, nil, nil)
	if err := s.mirror.Start(*attachAddr); err != nil {
		log.Fatalf("attach server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/mcp", s.mcp)
	mux.HandleFunc("/v1/state", s.state)
	mux.HandleFunc("/v1/state/events", s.stateEvents)
	mux.HandleFunc("/v1/memory/", s.memoryRoute)
	mux.HandleFunc("/v1/instances", s.instancesCollection)
	mux.HandleFunc("/v1/instances/", s.instance)

	go func(snapshot []agent.Instance) {
		if err := s.memory.SyncSnapshot(snapshot); err != nil {
			log.Printf("memory initial sync: %v", err)
		}
	}(store.Snapshot())

	log.Printf("clichat-host listening http=%s attach=%s state=%s memory=%s", *httpAddr, *attachAddr, *statePath, *memoryPath)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "clichat-state.json"
	}
	return filepath.Join(home, ".clichat", "state.json")
}

func defaultMemoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "clichat-memory.sqlite3"
	}
	return filepath.Join(home, ".clichat", "memory.sqlite3")
}

func (s *server) syncMemoryEvents(events []agent.Event) {
	if s.memory == nil {
		return
	}
	for _, event := range events {
		switch event.Kind {
		case agent.EventInstanceUpdated:
			inst, ok := event.Payload.(agent.Instance)
			if !ok {
				continue
			}
			if err := s.memory.SyncInstance(inst); err != nil {
				log.Printf("memory sync instance=%s: %v", event.ID, err)
			}
		case agent.EventInstanceRemoved:
			if event.ID != "" {
				if err := s.memory.DeleteConversation(event.ID); err != nil {
					log.Printf("memory delete instance=%s: %v", event.ID, err)
				}
			}
		}
	}
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": filterStateInstances(r, s.store.Snapshot())})
}

func (s *server) stateEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")

	out := make(chan []agent.Event, 32)
	_, unsubscribe := s.store.Subscribe(func(events []agent.Event) {
		// Recover from send-on-closed-channel: the store may invoke this
		// listener concurrently with handler exit. defers run in reverse
		// (unsubscribe then close), but a listener already in-flight can
		// still race the close.
		defer func() { _ = recover() }()
		select {
		case out <- events:
		default:
		}
	})
	defer close(out)
	defer unsubscribe()

	internalOnly := r.URL.Query().Get("origin") == string(agent.OriginInternal)
	writeSSE(w, "snapshot", map[string]any{"instances": filterStateInstances(r, s.store.Snapshot())})
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case events := <-out:
			for _, event := range events {
				if internalOnly && !internalStateEvent(event) {
					continue
				}
				writeSSE(w, string(event.Kind), event)
			}
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func filterStateInstances(r *http.Request, instances []agent.Instance) []agent.Instance {
	if r.URL.Query().Get("origin") != string(agent.OriginInternal) {
		return instances
	}
	out := make([]agent.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst.Origin == agent.OriginInternal {
			out = append(out, inst)
		}
	}
	return out
}

func internalStateEvent(event agent.Event) bool {
	if event.Kind != agent.EventInstanceUpdated {
		return true
	}
	inst, ok := event.Payload.(agent.Instance)
	return ok && inst.Origin == agent.OriginInternal
}

func (s *server) memoryRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(strings.Trim(r.URL.Path, "/"), "v1/memory/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := parts[0]
	action := parts[1]
	switch action {
	case "summary":
		s.memorySummary(w, r, id)
	case "search":
		s.memorySearch(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *server) memorySummary(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	inst, ok := s.store.Get(id)
	if ok && s.memory != nil {
		if err := s.memory.SyncInstance(inst); err != nil {
			log.Printf("memory summary sync instance=%s: %v", id, err)
		}
	}
	if s.memory != nil {
		mem, err := s.memory.Conversation(id)
		if err == nil {
			writeJSON(w, http.StatusOK, mem)
			return
		}
	}
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, memory.BuildConversationMemory(inst, 12000))
}

func (s *server) memorySearch(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := s.store.Get(id); !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	results, err := s.memory.SearchConversation(id, query, limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *server) instancesCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"instances": s.store.Snapshot()})
	case http.MethodPost:
		s.createInstance(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type createInstanceRequest struct {
	Origin          string `json:"origin"`
	ProviderID      string `json:"providerId"`
	Title           string `json:"title"`
	CWD             string `json:"cwd"`
	TTY             string `json:"tty"`
	PID             int    `json:"pid"`
	ClaudeSessionID string `json:"claudeSessionId"`
	TranscriptPath  string `json:"transcriptPath"`
}

func (s *server) createInstance(w http.ResponseWriter, r *http.Request) {
	var input createInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	switch agent.Origin(input.Origin) {
	case agent.OriginExternal:
		inst := s.store.RegisterExternal(agent.RegisterExternalInput{
			ProviderID:      input.ProviderID,
			Title:           input.Title,
			CWD:             input.CWD,
			TTY:             input.TTY,
			PID:             input.PID,
			ClaudeSessionID: input.ClaudeSessionID,
			TranscriptPath:  input.TranscriptPath,
		})
		writeJSON(w, http.StatusOK, inst)
	case agent.OriginInternal, "":
		inst := s.store.CreateInternal(agent.CreateInternalInput{
			ProviderID: input.ProviderID,
			Title:      input.Title,
			CWD:        input.CWD,
		})
		writeJSON(w, http.StatusOK, inst)
	default:
		writeError(w, http.StatusBadRequest, "origin must be internal or external")
	}
}

func (s *server) instance(w http.ResponseWriter, r *http.Request) {
	id, action, ok := splitInstancePath(r.URL.Path)
	if !ok || id == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch action {
	case "":
		s.instanceRoot(w, r, id)
	case "status":
		s.instanceStatus(w, r, id)
	case "provider":
		s.instanceProvider(w, r, id)
	case "message":
		s.instanceMessage(w, r, id)
	case "pending":
		s.instancePending(w, r, id)
	case "send":
		s.instanceSend(w, r, id)
	case "resize":
		s.instanceResize(w, r, id)
	case "events":
		s.instanceEvents(w, r, id)
	case "start-terminal":
		s.instanceStartTerminal(w, r, id)
	case "stop-terminal":
		s.instanceStopTerminal(w, r, id)
	case "attach-claude":
		s.instanceAttachClaude(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *server) instanceRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		inst, ok := s.store.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "instance not found")
			return
		}
		writeJSON(w, http.StatusOK, inst)
	case http.MethodDelete:
		_ = s.manager.Stop(id)
		s.store.Unregister(id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type statusRequest struct {
	Status string `json:"status"`
	Tool   string `json:"tool"`
}

type providerRequest struct {
	ProviderID string `json:"providerId"`
}

func (s *server) instanceProvider(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input providerRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(input.ProviderID) == "" {
		writeError(w, http.StatusBadRequest, "providerId is required")
		return
	}
	_ = s.manager.Stop(id)
	s.prompts.Clear(id)
	inst, ok := s.store.SetProvider(id, input.ProviderID)
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *server) instanceStatus(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input statusRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	inst, ok := s.store.SetStatus(id, agent.StatusInput{
		Status: agent.Status(input.Status),
		Tool:   input.Tool,
	})
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

type messageRequest struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func (s *server) instanceMessage(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input messageRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	role := agent.Role(strings.ToLower(strings.TrimSpace(input.Role)))
	if role == "" {
		role = agent.RoleAssistant
	}
	inst, _, ok := s.store.AppendMessage(id, agent.AppendInput{Role: role, Text: input.Text})
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

type pendingRequest struct {
	Question string                `json:"question"`
	Actions  []agent.PendingAction `json:"actions"`
}

func (s *server) instancePending(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		var input pendingRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		inst, ok := s.store.SetPending(id, input.Question, input.Actions)
		if !ok {
			writeError(w, http.StatusNotFound, "instance not found")
			return
		}
		writeJSON(w, http.StatusOK, inst)
	case http.MethodDelete:
		inst, ok := s.store.ClearPending(id)
		if !ok {
			writeError(w, http.StatusNotFound, "instance not found")
			return
		}
		writeJSON(w, http.StatusOK, inst)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type sendRequest struct {
	Text string `json:"text"`
	Data string `json:"data"`
}

func (s *server) instanceSend(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	inst, ok := s.store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.Origin != agent.OriginInternal {
		writeError(w, http.StatusBadRequest, "send only supported on internal instances")
		return
	}
	var input sendRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if input.Data != "" {
		decoded, err := base64.StdEncoding.DecodeString(input.Data)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid base64 data")
			return
		}
		if err := s.manager.Send(id, decoded); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
		return
	}
	if err := s.manager.SendLine(id, input.Text); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

type resizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s *server) instanceResize(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input resizeRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.manager.Resize(id, input.Cols, input.Rows); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type startTerminalRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
	Env     []string `json:"env"`
}

type attachClaudeRequest struct {
	ClaudeSessionID string `json:"claudeSessionId"`
	TranscriptPath  string `json:"transcriptPath"`
	TTY             string `json:"tty"`
	PID             int    `json:"pid"`
	CWD             string `json:"cwd"`
}

func (s *server) instanceStartTerminal(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	inst, ok := s.store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if inst.Origin != agent.OriginInternal {
		writeError(w, http.StatusBadRequest, "cannot start terminal for external instance")
		return
	}
	var input startTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if input.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	effectiveCWD := firstNonEmpty(input.CWD, inst.CWD)
	// Always rewrite empty / home-root CWDs to a dedicated sandbox dir to
	// avoid the spawned CLI (Claude/Codex/Gemini) scanning the home tree
	// and triggering macOS TCC prompts for FileProvider domains (Google
	// Drive, iCloud, etc.) and MediaLibrary (Apple Music). Older instances
	// were created with CWD=$HOME — auto-migrate them to the sandbox here.
	if home, err := os.UserHomeDir(); err == nil {
		if effectiveCWD == "" || effectiveCWD == home {
			sandbox := filepath.Join(home, ".clichat", "sandbox", id)
			_ = os.MkdirAll(sandbox, 0o755)
			effectiveCWD = sandbox
		}
	}
	// A fresh provider process always creates a fresh transcript. Clear any
	// previous transcript link so the watcher can claim the new file for this
	// internal chat instead of registering it as an external duplicate.
	s.store.SetTranscriptPath(id, "")
	var processPID int
	process, err := s.manager.Start(terminal.StartOptions{
		SessionID:  id,
		ProviderID: inst.ProviderID,
		Command:    input.Command,
		Args:       input.Args,
		CWD:        effectiveCWD,
		Env:        input.Env,
		OnExit: func(sessionID string, _ error) {
			s.manager.Forget(sessionID)
			s.store.MarkTerminalExited(sessionID, processPID)
			s.prompts.Clear(sessionID)
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	processPID = process.PID()
	// Persist the working directory so the JSONL→instance linker can match against it.
	s.store.UpdateCWD(id, effectiveCWD)
	updated, _ := s.store.SetTerminalAttached(id, true, processPID)
	go s.runPromptWatcher(id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) runPromptWatcher(sessionID string) {
	output, unsubscribe, err := s.manager.Subscribe(sessionID)
	if err != nil {
		return
	}
	defer unsubscribe()
	for chunk := range output {
		question, actions, ok := s.prompts.Feed(sessionID, chunk)
		if !ok {
			continue
		}
		s.store.SetPending(sessionID, question, actions)
	}
}

func (s *server) instanceAttachClaude(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input attachClaudeRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	updated, ok := s.store.AttachClaude(id, agent.AttachClaudeInput{
		ClaudeSessionID: input.ClaudeSessionID,
		TranscriptPath:  input.TranscriptPath,
		TTY:             input.TTY,
		PID:             input.PID,
		CWD:             input.CWD,
	})
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) instanceStopTerminal(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.manager.Stop(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.store.SetTerminalAttached(id, false, 0)
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) instanceEvents(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	output, unsubscribe, err := s.manager.Subscribe(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer unsubscribe()
	hostEvents, unsubscribeHost := s.subscribeEvents(id)
	defer unsubscribeHost()

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				writeEvent(w, "exit", nil, "")
				flusher.Flush()
				return
			}
			writeEvent(w, "output", chunk, "")
			flusher.Flush()
		case event := <-hostEvents:
			writeEvent(w, event.Type, event.Data, "")
			flusher.Flush()
		case <-ticker.C:
			writeEvent(w, "ping", nil, "")
			flusher.Flush()
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				log.Printf("events %s: %v", id, ctx.Err())
			}
			return
		}
	}
}

func (s *server) subscribeEvents(sessionID string) (<-chan hostEvent, func()) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	s.nextSubID++
	id := s.nextSubID
	ch := make(chan hostEvent, 32)
	if s.eventSubs[sessionID] == nil {
		s.eventSubs[sessionID] = make(map[int]chan hostEvent)
	}
	s.eventSubs[sessionID][id] = ch
	return ch, func() {
		s.eventsMu.Lock()
		defer s.eventsMu.Unlock()
		if subs := s.eventSubs[sessionID]; subs != nil {
			if sub, ok := subs[id]; ok {
				delete(subs, id)
				close(sub)
			}
			if len(subs) == 0 {
				delete(s.eventSubs, sessionID)
			}
		}
	}
}

func (s *server) emit(sessionID string, event hostEvent) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	for _, sub := range s.eventSubs[sessionID] {
		select {
		case sub <- event:
		default:
		}
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *server) mcp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "clichat", "version": "0.2.0"},
		})
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "agent_chat_set_topic",
					"description": "Update the short topic shown as the chat name in CLIchat. Call at the start of every new task and whenever the focus shifts. Keep it 2-6 words in the user's language.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"topic"},
						"properties": map[string]any{
							"session_id":        map[string]any{"type": "string"},
							"claude_session_id": map[string]any{"type": "string"},
							"topic":             map[string]any{"type": "string"},
						},
					},
				},
				{
					"name":        "agent_chat_register",
					"description": "Register a Claude Code session with CLIchat. Call once on session start. Returns the agent-chat instance id.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"claude_session_id": map[string]any{"type": "string"},
							"title":             map[string]any{"type": "string"},
							"cwd":               map[string]any{"type": "string"},
							"transcript_path":   map[string]any{"type": "string"},
						},
					},
				},
				{
					"name":        "agent_chat_reply",
					"description": "Send the exact user-facing assistant reply to the CLIchat UI. Call once per final response.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"text"},
						"properties": map[string]any{
							"session_id":        map[string]any{"type": "string"},
							"claude_session_id": map[string]any{"type": "string"},
							"text":              map[string]any{"type": "string"},
						},
					},
				},
				{
					"name":        "agent_chat_question",
					"description": "Ask the user for confirmation/input in the CLIchat UI.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"question"},
						"properties": map[string]any{
							"session_id":        map[string]any{"type": "string"},
							"claude_session_id": map[string]any{"type": "string"},
							"question":          map[string]any{"type": "string"},
						},
					},
				},
			},
		})
	case "tools/call":
		var params toolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid tool params")
			return
		}
		switch params.Name {
		case "agent_chat_set_topic":
			id := s.resolveInstance(params.Arguments)
			if id == "" {
				writeRPCError(w, req.ID, -32602, "session not found")
				return
			}
			topic := stringArg(params.Arguments, "topic")
			s.store.SetTopic(id, topic)
			writeToolResult(w, req.ID, "ok")
		case "agent_chat_register":
			inst := s.store.RegisterExternal(agent.RegisterExternalInput{
				ProviderID:      "claude",
				Title:           stringArg(params.Arguments, "title"),
				CWD:             stringArg(params.Arguments, "cwd"),
				ClaudeSessionID: stringArg(params.Arguments, "claude_session_id"),
				TranscriptPath:  stringArg(params.Arguments, "transcript_path"),
			})
			writeToolResult(w, req.ID, fmt.Sprintf("registered:%s", inst.ID))
		case "agent_chat_reply":
			id := s.resolveInstance(params.Arguments)
			if id == "" {
				writeRPCError(w, req.ID, -32602, "session not found")
				return
			}
			text := strings.TrimSpace(stringArg(params.Arguments, "text"))
			if text != "" {
				s.store.AppendMessage(id, agent.AppendInput{Role: agent.RoleAssistant, Text: text})
				s.emit(id, hostEvent{Type: "chat", Data: []byte(text)})
			}
			writeToolResult(w, req.ID, "ok")
		case "agent_chat_question":
			id := s.resolveInstance(params.Arguments)
			if id == "" {
				writeRPCError(w, req.ID, -32602, "session not found")
				return
			}
			question := strings.TrimSpace(stringArg(params.Arguments, "question"))
			if question != "" {
				s.store.SetPending(id, question, []agent.PendingAction{
					{ID: "yes", Label: "Yes", Input: "y"},
					{ID: "no", Label: "No", Input: "n"},
				})
				s.emit(id, hostEvent{Type: "question", Data: []byte(question)})
			}
			writeToolResult(w, req.ID, "ok")
		default:
			writeRPCError(w, req.ID, -32602, "unknown tool")
		}
	default:
		writeRPCError(w, req.ID, -32601, "method not found")
	}
}

func (s *server) resolveInstance(args map[string]any) string {
	if id := stringArg(args, "session_id"); id != "" {
		if _, ok := s.store.Get(id); ok {
			return id
		}
	}
	if claudeID := stringArg(args, "claude_session_id"); claudeID != "" {
		if inst, ok := s.store.FindByClaudeSessionID(claudeID); ok {
			return inst.ID
		}
	}
	return ""
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if value, ok := args[key].(string); ok {
		return value
	}
	return ""
}

func writeToolResult(w http.ResponseWriter, id any, text string) {
	writeRPCResult(w, id, map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func splitInstancePath(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "instances" {
		return "", "", false
	}
	if len(parts) == 3 {
		return parts[2], "", true
	}
	if len(parts) == 4 {
		return parts[2], parts[3], true
	}
	return "", "", false
}

func writeEvent(w http.ResponseWriter, kind string, data []byte, message string) {
	payload := map[string]string{"type": kind}
	if len(data) > 0 {
		payload["data"] = base64.StdEncoding.EncodeToString(data)
	}
	if message != "" {
		payload["error"] = message
	}
	encoded, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
}

func writeSSE(w http.ResponseWriter, kind string, payload any) {
	encoded, _ := json.Marshal(map[string]any{"kind": kind, "payload": payload})
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, encoded)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
