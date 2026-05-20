package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptWatcherClaimsCodexTranscriptForTransferredInternalChat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, ".clichat", "sandbox")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	internal := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})
	if _, appended, ok := store.AppendMessage(internal.ID, AppendInput{Role: RoleUser, Text: "hello"}); !ok || !appended {
		t.Fatalf("expected direct user message")
	}

	sessionStarted := time.Now().Add(-2 * time.Second).UTC()
	store.mu.Lock()
	store.instances[internal.ID].CreatedAt = sessionStarted.Add(-8 * time.Hour).Format(time.RFC3339Nano)
	store.instances[internal.ID].UpdatedAt = sessionStarted.Add(-1 * time.Second).Format(time.RFC3339Nano)
	store.instances[internal.ID].TerminalAttached = true
	store.mu.Unlock()

	transcript := filepath.Join(home, ".codex", "sessions", "2026", "05", "20", "rollout-2026-05-20T13-10-26-019e4627-14fb-7213-a683-e6a8babd22b5.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = store.RegisterExternal(RegisterExternalInput{
		ProviderID:        "codex",
		CWD:               cwd,
		ProviderSessionID: "019e4627-14fb-7213-a683-e6a8babd22b5",
		TranscriptPath:    transcript,
	})

	lines := []string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"id":"019e4627-14fb-7213-a683-e6a8babd22b5","cwd":%q,"timestamp":%q}}`, cwd, sessionStarted.Format(time.RFC3339Nano)),
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<clichat-handoff>\nstartup context\n</clichat-handoff>"}]}}`,
		`{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer from codex"}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	watcher := NewTranscriptWatcher(store)
	watcher.discover()
	watcher.poll()

	got, ok := store.Get(internal.ID)
	if !ok {
		t.Fatalf("internal chat disappeared")
	}
	if got.TranscriptPath != transcript {
		t.Fatalf("TranscriptPath = %q, want %q", got.TranscriptPath, transcript)
	}
	if got.Status != StatusIdle {
		t.Fatalf("Status = %q, want %q", got.Status, StatusIdle)
	}
	if _, ok := store.FindByProviderSessionID("codex", "019e4627-14fb-7213-a683-e6a8babd22b5"); !ok {
		t.Fatalf("provider session id was not linked")
	}

	var helloCount int
	var sawAssistant bool
	for _, msg := range got.Messages {
		if msg.Role == RoleUser && msg.Text == "hello" {
			helloCount++
		}
		if msg.Role == RoleAssistant && msg.Text == "answer from codex" {
			sawAssistant = true
		}
		if strings.Contains(msg.Text, "clichat-handoff") {
			t.Fatalf("internal handoff leaked into visible messages: %q", msg.Text)
		}
	}
	if helloCount != 1 {
		t.Fatalf("hello message count = %d, want 1", helloCount)
	}
	if !sawAssistant {
		t.Fatalf("assistant response was not appended to the internal chat")
	}
}
