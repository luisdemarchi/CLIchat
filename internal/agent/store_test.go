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
	store.instances[older.ID].UpdatedAt = store.instances[older.ID].CreatedAt
	store.instances[newer.ID].UpdatedAt = store.instances[newer.ID].CreatedAt
	store.instances[older.ID].TerminalAttached = true
	store.instances[newer.ID].TerminalAttached = true
	store.mu.Unlock()

	got, ok := store.FindAwaitingTranscript("codex", cwd, sessionStarted.Add(-2*time.Minute))
	if !ok {
		t.Fatalf("expected a candidate")
	}
	if got.ID != newer.ID {
		t.Fatalf("FindAwaitingTranscript chose %s, want %s", got.ID, newer.ID)
	}
}

func TestFindAwaitingTranscriptUsesTerminalUpdatedAtForTransferredChat(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "sandbox")
	inst := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})

	sessionStarted := time.Date(2026, 5, 20, 13, 10, 0, 0, time.UTC)
	store.mu.Lock()
	store.instances[inst.ID].CreatedAt = sessionStarted.Add(-6 * time.Hour).Format(time.RFC3339Nano)
	store.instances[inst.ID].UpdatedAt = sessionStarted.Add(-3 * time.Second).Format(time.RFC3339Nano)
	store.instances[inst.ID].TerminalAttached = true
	store.mu.Unlock()

	got, ok := store.FindAwaitingTranscript("codex", cwd, sessionStarted.Add(-2*time.Minute))
	if !ok {
		t.Fatalf("expected transferred chat to match by terminal update time")
	}
	if got.ID != inst.ID {
		t.Fatalf("FindAwaitingTranscript chose %s, want %s", got.ID, inst.ID)
	}
}

func TestClaimTranscriptForInternalRemovesDuplicateExternal(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "sandbox")
	internal := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})
	transcript := filepath.Join(t.TempDir(), "rollout-2026-05-20T13-10-26-019e4627-14fb-7213-a683-e6a8babd22b5.jsonl")
	external := store.RegisterExternal(RegisterExternalInput{
		ProviderID:        "codex",
		CWD:               cwd,
		ProviderSessionID: "019e4627-14fb-7213-a683-e6a8babd22b5",
		TranscriptPath:    transcript,
	})

	sessionStarted := time.Date(2026, 5, 20, 13, 10, 26, 0, time.UTC)
	store.mu.Lock()
	store.instances[internal.ID].CreatedAt = sessionStarted.Add(-8 * time.Hour).Format(time.RFC3339Nano)
	store.instances[internal.ID].UpdatedAt = sessionStarted.Add(-4 * time.Second).Format(time.RFC3339Nano)
	store.instances[internal.ID].TerminalAttached = true
	store.mu.Unlock()

	claimed, ok := store.ClaimTranscriptForInternal("codex", transcript, "019e4627-14fb-7213-a683-e6a8babd22b5", cwd, sessionStarted.Add(-2*time.Minute))
	if !ok {
		t.Fatalf("expected transcript claim")
	}
	if claimed.ID != internal.ID {
		t.Fatalf("claimed %s, want %s", claimed.ID, internal.ID)
	}
	if claimed.TranscriptPath != transcript {
		t.Fatalf("TranscriptPath = %q, want %q", claimed.TranscriptPath, transcript)
	}
	if _, ok := store.Get(external.ID); ok {
		t.Fatalf("duplicate external instance was not removed")
	}
}

func TestAppendMessageDedupsSourcefulDuplicateText(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	inst := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: t.TempDir()})
	if _, appended, ok := store.AppendMessage(inst.ID, AppendInput{Role: RoleUser, Text: "hello", SourceID: "a"}); !ok || !appended {
		t.Fatalf("expected first append")
	}
	if _, appended, ok := store.AppendMessage(inst.ID, AppendInput{Role: RoleUser, Text: "hello", SourceID: "b"}); !ok {
		t.Fatalf("second append failed")
	} else if appended {
		t.Fatalf("expected duplicate sourceful text to be ignored")
	}
}
