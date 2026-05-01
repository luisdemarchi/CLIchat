package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func watcherLog(format string, args ...interface{}) {
	log.Printf(format, args...)
}

func safeID(id string) string { return shortID(id) }

// systemContentTag matches a leading XML-ish tag that Claude Code injects into
// the transcript for plumbing (background task results, hook output, slash
// commands, etc). When a transcript entry starts with one of these tags we
// drop it instead of rendering it as a user/assistant bubble.
var systemContentTag = regexp.MustCompile(`(?s)^\s*<([a-z][a-z0-9-]*)>`)

var systemTagSet = map[string]struct{}{
	"task-notification":       {},
	"system-reminder":         {},
	"command-name":            {},
	"command-message":         {},
	"command-args":            {},
	"local-command-stdout":    {},
	"local-command-stderr":    {},
	"user-prompt-submit-hook": {},
	"bash-input":              {},
	"bash-stdout":             {},
	"bash-stderr":             {},
	"bash-stdin-disabled":     {},
	"function_calls":          {},
	"function_results":        {},
}

func isSystemTranscriptEntry(text string) bool {
	m := systemContentTag.FindStringSubmatch(text)
	if m == nil {
		return false
	}
	_, ok := systemTagSet[m[1]]
	return ok
}

const maxTranscriptLine = 4 * 1024 * 1024

type TranscriptEntry struct {
	Type            string
	Role            Role
	Text            string
	Tool            string
	ClaudeSessionID string
}

type transcriptCursor struct {
	path   string
	offset int64
	seen   map[string]bool
}

type TranscriptWatcher struct {
	store    *Store
	mu       sync.Mutex
	cursors  map[string]*transcriptCursor
	stop     chan struct{}
	stopOnce sync.Once
}

func NewTranscriptWatcher(store *Store) *TranscriptWatcher {
	return &TranscriptWatcher{
		store:   store,
		cursors: make(map[string]*transcriptCursor),
		stop:    make(chan struct{}),
	}
}

func (w *TranscriptWatcher) Start(ctx context.Context) {
	go w.run(ctx)
}

func (w *TranscriptWatcher) run(ctx context.Context) {
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()

	discoveryTicker := time.NewTicker(3 * time.Second)
	defer discoveryTicker.Stop()

	w.discover()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case <-discoveryTicker.C:
			w.discover()
		case <-ticker.C:
			w.poll()
		}
	}
}

func (w *TranscriptWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
}

// discover scans provider transcript folders and registers recent external
// sessions. Codex/Gemini transcripts are never attached to internal chats by
// heuristic because those global folders also contain this Codex process and
// other unrelated terminal sessions.
func (w *TranscriptWatcher) discover() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-5 * time.Minute)

	rootClaude := filepath.Join(home, ".claude", "projects")
	_ = filepath.WalkDir(rootClaude, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		claudeID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if _, ok := w.store.FindByClaudeSessionID(claudeID); ok {
			w.track(path)
			return nil
		}
		cwd := projectDirFromHash(filepath.Base(filepath.Dir(path)))
		w.store.RegisterExternal(RegisterExternalInput{
			ProviderID:      "claude",
			CWD:             cwd,
			ClaudeSessionID: claudeID,
			TranscriptPath:  path,
		})
		w.track(path)
		return nil
	})

	rootCodex := filepath.Join(home, ".codex", "sessions")
	_ = filepath.WalkDir(rootCodex, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if !strings.HasPrefix(filepath.Base(path), "rollout-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		if _, ok := w.store.FindByTranscriptPath(path); ok {
			w.track(path)
			return nil
		}
		cwd, sessionStarted := codexSessionMeta(path)
		// 1) try to attach to a still-unlinked internal Codex chat that matches CWD + time window.
		if cwd != "" {
			if inst, ok := w.store.FindAwaitingTranscript("codex", cwd, sessionStarted.Add(-2*time.Minute)); ok {
				w.store.SetTranscriptPath(inst.ID, path)
				w.track(path)
				return nil
			}
		}
		// 2) otherwise it is a Codex run started outside the app — register external (UI hides it).
		w.store.RegisterExternal(RegisterExternalInput{
			ProviderID:     "codex",
			CWD:            cwd,
			TranscriptPath: path,
		})
		w.track(path)
		return nil
	})

	rootGemini := filepath.Join(home, ".gemini", "tmp")
	_ = filepath.WalkDir(rootGemini, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if !strings.HasPrefix(filepath.Base(path), "session-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		if _, ok := w.store.FindByTranscriptPath(path); ok {
			w.track(path)
			return nil
		}
		// Gemini buckets sessions under tmp/<sha-of-cwd>/chats/. We don't have the cwd
		// directly in the file, but the linking heuristic falls back on file mtime vs.
		// internal CreatedAt; matching is only attempted when an internal Gemini chat
		// is awaiting a transcript and was created within ~2min of the JSONL.
		cwdHint := geminiCWDHint(path)
		if cwdHint != "" {
			if inst, ok := w.store.FindAwaitingTranscript("gemini", cwdHint, info.ModTime().Add(-2*time.Minute)); ok {
				w.store.SetTranscriptPath(inst.ID, path)
				w.track(path)
				return nil
			}
		}
		w.store.RegisterExternal(RegisterExternalInput{
			ProviderID:     "gemini",
			CWD:            cwdHint,
			TranscriptPath: path,
		})
		w.track(path)
		return nil
	})
}

// codexSessionMeta reads the first JSONL line of a rollout file and returns
// (cwd, started_at) extracted from the `session_meta` payload. Empty values on
// any read/parse failure.
func codexSessionMeta(path string) (string, time.Time) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	if !scanner.Scan() {
		return "", time.Time{}
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			CWD       string `json:"cwd"`
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil || record.Type != "session_meta" {
		return "", time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, record.Payload.Timestamp)
	return record.Payload.CWD, t
}

// geminiCWDHint returns a best-effort CWD inferred from the parent directory
// name of a Gemini transcript. Returns "" when the heuristic cannot extract
// something safe to match against an internal instance.
func geminiCWDHint(path string) string {
	parent := filepath.Base(filepath.Dir(filepath.Dir(path)))
	if parent == "" || parent == "tmp" || parent == "stitch" {
		return ""
	}
	return parent
}

func (w *TranscriptWatcher) track(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, tracked := w.cursors[path]; !tracked {
		w.cursors[path] = &transcriptCursor{path: path, seen: make(map[string]bool)}
	}
}

func (w *TranscriptWatcher) poll() {
	w.mu.Lock()
	cursors := make([]*transcriptCursor, 0, len(w.cursors))
	for _, cursor := range w.cursors {
		cursors = append(cursors, cursor)
	}
	w.mu.Unlock()

	for _, cursor := range cursors {
		entries, nextOffset, ok := readTranscript(cursor.path, cursor.offset)
		if !ok {
			continue
		}
		cursor.offset = nextOffset

		var inst Instance
		var found bool
		if strings.Contains(cursor.path, ".claude") {
			claudeID := strings.TrimSuffix(filepath.Base(cursor.path), ".jsonl")
			inst, found = w.store.FindByClaudeSessionID(claudeID)
		} else {
			inst, found = w.store.FindByTranscriptPath(cursor.path)
		}

		if !found {
			continue
		}

		for _, entry := range entries {
			if entry.Text == "" {
				continue
			}
			if isSystemTranscriptEntry(entry.Text) {
				continue
			}
			// Dedup within this cursor session
			if cursor.seen[entry.Text] {
				continue
			}
			cursor.seen[entry.Text] = true

			preview := entry.Text
			if len(preview) > 50 {
				preview = preview[:50]
			}
			watcherLog("transcript→AppendMessage instance=%s provider=%s path=%s role=%s text=%q",
				safeID(inst.ID), inst.ProviderID, filepath.Base(cursor.path), entry.Role, preview)
			w.store.AppendMessage(inst.ID, AppendInput{Role: entry.Role, Text: entry.Text})
			if entry.Role == RoleAssistant {
				if entry.Tool != "" {
					w.store.SetStatus(inst.ID, StatusInput{Status: StatusBusy, Tool: entry.Tool})
				}
				// Also try to detect prompts in the transcript text itself (e.g. Claude's "y/n" questions)
				if question, actions, ok := DetectPrompt(entry.Text); ok {
					w.store.SetPending(inst.ID, question, actions)
				}
			}
		}
	}
}

func readTranscript(path string, offset int64) ([]TranscriptEntry, int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, false
	}
	defer file.Close()
	if _, err := file.Seek(offset, 0); err != nil {
		return nil, offset, false
	}

	provider := providerFromTranscriptPath(path)

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	var entries []TranscriptEntry
	for scanner.Scan() {
		entry, ok := parseTranscriptLine(scanner.Bytes(), provider)
		if ok {
			entries = append(entries, entry)
		}
	}
	next, err := file.Seek(0, 1)
	if err != nil {
		return entries, offset, false
	}
	return entries, next, true
}

func parseTranscriptLine(line []byte, provider string) (TranscriptEntry, bool) {
	switch provider {
	case "claude":
		return parseClaudeLine(line)
	case "codex":
		return parseCodexLine(line)
	case "gemini":
		return parseGeminiLine(line)
	default:
		return TranscriptEntry{}, false
	}
}

func providerFromTranscriptPath(path string) string {
	if strings.Contains(path, ".codex") {
		return "codex"
	}
	if strings.Contains(path, ".gemini") {
		return "gemini"
	}
	return "claude"
}

func parseClaudeLine(line []byte) (TranscriptEntry, bool) {
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return TranscriptEntry{}, false
	}

	entry := TranscriptEntry{Type: record.Type, ClaudeSessionID: record.SessionID}
	switch record.Type {
	case "assistant":
		entry.Role = RoleAssistant
		entry.Text, entry.Tool = extractTextAndTool(record.Message.Content)
	case "user":
		entry.Role = RoleUser
		entry.Text, _ = extractTextAndTool(record.Message.Content)
	default:
		return TranscriptEntry{}, false
	}
	return entry, true
}

func parseCodexLine(line []byte) (TranscriptEntry, bool) {
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return TranscriptEntry{}, false
	}

	entry := TranscriptEntry{Type: record.Type}
	if record.Type == "event_msg" && record.Payload.Type == "agent_message" {
		entry.Role = RoleAssistant
		entry.Text = record.Payload.Message
		return entry, true
	}
	if record.Type == "response_item" && record.Payload.Type == "message" {
		if record.Payload.Role == "assistant" {
			entry.Role = RoleAssistant
			var texts []string
			for _, c := range record.Payload.Content {
				if c.Type == "output_text" || c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			entry.Text = strings.Join(texts, "\n")
			return entry, true
		}
	}
	// For user messages in Codex, we might need another pattern if they appear differently.
	// But usually they are also in the JSONL.
	return TranscriptEntry{}, false
}

func parseGeminiLine(line []byte) (TranscriptEntry, bool) {
	var record struct {
		Type    string `json:"type"`
		Content any    `json:"content"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return TranscriptEntry{}, false
	}

	entry := TranscriptEntry{Type: record.Type}
	switch record.Type {
	case "gemini":
		entry.Role = RoleAssistant
		if s, ok := record.Content.(string); ok {
			entry.Text = s
		}
	case "user":
		entry.Role = RoleUser
		if s, ok := record.Content.(string); ok {
			entry.Text = s
		} else if blocks, ok := record.Content.([]any); ok {
			var texts []string
			for _, b := range blocks {
				if m, ok := b.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						texts = append(texts, t)
					}
				}
			}
			entry.Text = strings.Join(texts, "")
		}
	default:
		return TranscriptEntry{}, false
	}
	return entry, true
}

func extractTextAndTool(content any) (string, string) {
	switch value := content.(type) {
	case string:
		return strings.TrimSpace(value), ""
	case []any:
		var texts []string
		var tool string
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					texts = append(texts, strings.TrimSpace(text))
				}
			case "tool_use":
				if name, ok := block["name"].(string); ok {
					tool = name
				}
			}
		}
		return strings.TrimSpace(strings.Join(texts, "\n\n")), tool
	default:
		return "", ""
	}
}

// projectDirFromHash converts ~/.claude/projects/-Users-luis-foo-bar/ back to /Users/luis/foo/bar.
// Best-effort heuristic; result is opaque to the user but stable per project.
func projectDirFromHash(hash string) string {
	if hash == "" {
		return ""
	}
	hash = strings.TrimLeft(hash, "-")
	return "/" + strings.ReplaceAll(hash, "-", "/")
}
