package memory

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luisdemarchi/CLIchat/internal/agent"
)

func TestStoreSyncSummaryAndSearch(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "memory.sqlite3")
	store, err := New(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	inst := agent.Instance{
		ID:         "chat-1",
		ProviderID: "claude",
		Origin:     agent.OriginInternal,
		Title:      "Claude 1",
		CreatedAt:  "2026-05-20T00:00:00Z",
		UpdatedAt:  "2026-05-20T00:00:02Z",
		Messages: []agent.Message{
			{ID: "m1", Role: agent.RoleUser, Text: "refactor CLIchat to transfer conversations between terminals", CreatedAt: "2026-05-20T00:00:01Z"},
			{ID: "m2", Role: agent.RoleAssistant, Text: "I will adjust the terminal handoff", CreatedAt: "2026-05-20T00:00:02Z"},
		},
	}
	if err := store.SyncInstance(inst); err != nil {
		t.Fatalf("sync: %v", err)
	}

	mem, err := store.Conversation("chat-1")
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	if mem.MessageCount != 2 || !strings.Contains(mem.Summary, "CLIchat") {
		t.Fatalf("unexpected memory: %+v", mem)
	}
	if !strings.Contains(mem.Topic, "CLIchat") {
		t.Fatalf("unexpected topic: %q", mem.Topic)
	}

	results, err := store.SearchConversation("chat-1", "handoff terminal", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search result")
	}
}
