package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/terminal"
)

const (
	defaultHTTPAddress = "127.0.0.1:47657"
	defaultTTYAddress  = "127.0.0.1:47656"
)

type server struct {
	manager   *terminal.Manager
	mirror    *mirror.Server
	eventsMu  sync.Mutex
	nextSubID int
	eventSubs map[string]map[int]chan hostEvent
}

type hostEvent struct {
	Type string
	Data []byte
}

type startRequest struct {
	SessionID string   `json:"sessionId"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	CWD       string   `json:"cwd"`
}

type startResponse struct {
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
}

type sendRequest struct {
	Text string `json:"text"`
	Data string `json:"data"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: agent-host serve")
		os.Exit(2)
	}

	manager := terminal.NewManager()
	s := &server{manager: manager, eventSubs: make(map[string]map[int]chan hostEvent)}
	s.mirror = mirror.NewServer(manager, nil, nil)
	if err := s.mirror.Start(defaultTTYAddress); err != nil {
		log.Fatalf("attach server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/mcp", s.mcp)
	mux.HandleFunc("/v1/sessions", s.sessions)
	mux.HandleFunc("/v1/sessions/", s.session)

	log.Printf("agent-host listening http=%s attach=%s", defaultHTTPAddress, defaultTTYAddress)
	if err := http.ListenAndServe(defaultHTTPAddress, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var input startRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if input.SessionID == "" || input.Command == "" {
		writeError(w, http.StatusBadRequest, "sessionId and command are required")
		return
	}

	process, err := s.manager.Start(terminal.StartOptions{
		SessionID: input.SessionID,
		Command:   input.Command,
		Args:      input.Args,
		CWD:       input.CWD,
		OnExit: func(sessionID string, _ error) {
			s.manager.Forget(sessionID)
		},
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, startResponse{SessionID: input.SessionID, PID: process.PID()})
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	sessionID, action, ok := parseSessionPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch action {
	case "send":
		s.send(w, r, sessionID)
	case "events":
		s.events(w, r, sessionID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *server) send(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
		if err := s.manager.Send(sessionID, decoded); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
		return
	}
	if err := s.manager.SendLine(sessionID, input.Text); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *server) events(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	output, unsubscribe, err := s.manager.Subscribe(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer unsubscribe()
	hostEvents, unsubscribeHost := s.subscribeEvents(sessionID)
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
				log.Printf("events %s: %v", sessionID, ctx.Err())
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
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "agent-chat-local",
				"version": "0.1.0",
			},
		})
	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "agent_chat_reply",
					"description": "Send the exact user-facing assistant reply to the Agent Chat Local UI. Call once per final response.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"session_id", "text"},
						"properties": map[string]any{
							"session_id": map[string]any{"type": "string"},
							"text":       map[string]any{"type": "string"},
						},
					},
				},
				{
					"name":        "agent_chat_question",
					"description": "Ask the user for confirmation/input in the Agent Chat Local UI.",
					"inputSchema": map[string]any{
						"type":     "object",
						"required": []string{"session_id", "question"},
						"properties": map[string]any{
							"session_id": map[string]any{"type": "string"},
							"question":   map[string]any{"type": "string"},
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
		sessionID := stringArg(params.Arguments, "session_id")
		if sessionID == "" {
			writeRPCError(w, req.ID, -32602, "session_id is required")
			return
		}
		switch params.Name {
		case "agent_chat_reply":
			text := strings.TrimSpace(stringArg(params.Arguments, "text"))
			if text != "" {
				s.emit(sessionID, hostEvent{Type: "chat", Data: []byte(text)})
			}
			writeToolResult(w, req.ID, "ok")
		case "agent_chat_question":
			question := strings.TrimSpace(stringArg(params.Arguments, "question"))
			if question != "" {
				s.emit(sessionID, hostEvent{Type: "question", Data: []byte(question)})
			}
			writeToolResult(w, req.ID, "ok")
		default:
			writeRPCError(w, req.ID, -32602, "unknown tool")
		}
	default:
		writeRPCError(w, req.ID, -32601, "method not found")
	}
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
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func parseSessionPath(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "sessions" {
		return "", "", false
	}
	return parts[2], parts[3], true
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
