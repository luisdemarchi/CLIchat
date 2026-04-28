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
	"time"

	"agent-chat-local/internal/mirror"
	"agent-chat-local/internal/terminal"
)

const (
	defaultHTTPAddress = "127.0.0.1:47657"
	defaultTTYAddress  = "127.0.0.1:47656"
)

type server struct {
	manager *terminal.Manager
	mirror  *mirror.Server
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
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: agent-host serve")
		os.Exit(2)
	}

	manager := terminal.NewManager()
	s := &server{manager: manager}
	s.mirror = mirror.NewServer(manager, nil, nil)
	if err := s.mirror.Start(defaultTTYAddress); err != nil {
		log.Fatalf("attach server: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.health)
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
	if strings.TrimSpace(input.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
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
