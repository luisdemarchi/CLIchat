package agent

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFindAwaitingTranscriptChoosesNearestCreatedAt(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "project")
	older := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})
	newer := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})

	sessionStarted := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	store.mu.Lock()
	store.instances[older.ID].CreatedAt = sessionStarted.Add(-90 * time.Second).Format(time.RFC3339Nano)
	store.instances[newer.ID].CreatedAt = sessionStarted.Add(-2 * time.Second).Format(time.RFC3339Nano)
	store.mu.Unlock()

	got, ok := store.FindAwaitingTranscript("codex", cwd, sessionStarted.Add(-2*time.Minute))
	if !ok {
		t.Fatalf("expected a candidate")
	}
	if got.ID != newer.ID {
		t.Fatalf("FindAwaitingTranscript chose %s, want %s", got.ID, newer.ID)
	}
}
