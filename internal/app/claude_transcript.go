package app

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-chat-local/internal/session"
)

const maxClaudeTranscriptLine = 4 * 1024 * 1024

func (a *App) startClaudeTranscriptWatcher(sessionID string, cwd string) {
	a.transcriptMu.Lock()
	if cancel := a.transcripts[sessionID]; cancel != nil {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.transcripts[sessionID] = cancel
	a.transcriptMu.Unlock()

	startedAt := time.Now().Add(-5 * time.Second)
	go a.watchClaudeTranscript(ctx, sessionID, cwd, startedAt)
}

func (a *App) watchClaudeTranscript(ctx context.Context, sessionID string, cwd string, startedAt time.Time) {
	var path string
	var offset int64
	seen := make(map[string]bool)
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if path == "" {
				if found := findClaudeTranscript(cwd, startedAt); found != "" {
					path = found
				} else {
					continue
				}
			}
			nextOffset, texts, ok := readClaudeTranscript(path, offset)
			if !ok {
				path = ""
				offset = 0
				continue
			}
			offset = nextOffset
			for _, text := range texts {
				text = strings.TrimSpace(text)
				if text == "" || seen[text] {
					continue
				}
				seen[text] = true
				if _, added, _ := a.registry.AppendAssistantIfNew(sessionID, text); added {
					_, _ = a.registry.SetStatus(sessionID, session.Idle, "")
					a.emitState()
				}
			}
		}
	}
}

func findClaudeTranscript(cwd string, since time.Time) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".claude", "projects")
	var newestPath string
	var newestMod time.Time

	if strings.TrimSpace(cwd) != "" {
		projectHash := strings.TrimLeft(strings.ReplaceAll(cwd, "/", "-"), "-")
		candidateDir := filepath.Join(root, projectHash)
		if path, mod := newestJSONL(candidateDir, since); path != "" {
			return path
		} else if !mod.IsZero() {
			newestMod = mod
		}
	}

	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(since) {
			return nil
		}
		if newestPath == "" || info.ModTime().After(newestMod) {
			newestPath = path
			newestMod = info.ModTime()
		}
		return nil
	})
	return newestPath
}

func newestJSONL(dir string, since time.Time) (string, time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}
	}
	var newestPath string
	var newestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		if newestPath == "" || info.ModTime().After(newestMod) {
			newestPath = filepath.Join(dir, entry.Name())
			newestMod = info.ModTime()
		}
	}
	return newestPath, newestMod
}

func readClaudeTranscript(path string, offset int64) (int64, []string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return offset, nil, false
	}
	defer file.Close()

	if _, err := file.Seek(offset, 0); err != nil {
		return offset, nil, false
	}

	var texts []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxClaudeTranscriptLine)
	for scanner.Scan() {
		if text := assistantTextFromJSONL(scanner.Bytes()); text != "" {
			texts = append(texts, text)
		}
	}
	next, err := file.Seek(0, 1)
	if err != nil {
		return offset, texts, false
	}
	return next, texts, true
}

func assistantTextFromJSONL(line []byte) string {
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &record); err != nil || record.Type != string(session.Assistant) {
		return ""
	}

	switch content := record.Message.Content.(type) {
	case string:
		return strings.TrimSpace(content)
	case []any:
		var parts []string
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	default:
		return ""
	}
}
