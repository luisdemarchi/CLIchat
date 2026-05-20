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

func TestClaimTranscriptForInternalMergesDuplicateExternalMessages(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "sandbox")
	internal := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})
	transcript := filepath.Join(t.TempDir(), "rollout-2026-05-20T14-19-45-019e4666-87cf-7932-aa2a-e14d7b229175.jsonl")
	external := store.RegisterExternal(RegisterExternalInput{
		ProviderID:        "codex",
		CWD:               cwd,
		ProviderSessionID: "019e4666-87cf-7932-aa2a-e14d7b229175",
		TranscriptPath:    transcript,
	})
	if _, appended, ok := store.AppendMessage(external.ID, AppendInput{Role: RoleAssistant, Text: "Amanhã será quinta-feira.", SourceID: transcript + ":100"}); !ok || !appended {
		t.Fatalf("expected external assistant append")
	}

	sessionStarted := time.Date(2026, 5, 20, 14, 19, 45, 0, time.UTC)
	store.mu.Lock()
	store.instances[internal.ID].UpdatedAt = sessionStarted.Add(2 * time.Second).Format(time.RFC3339Nano)
	store.instances[internal.ID].TerminalAttached = true
	store.instances[internal.ID].Status = StatusBusy
	store.mu.Unlock()

	claimed, ok := store.ClaimTranscriptForInternal("codex", transcript, "019e4666-87cf-7932-aa2a-e14d7b229175", cwd, sessionStarted.Add(-2*time.Minute))
	if !ok {
		t.Fatalf("expected transcript claim")
	}
	if _, ok := store.Get(external.ID); ok {
		t.Fatalf("duplicate external instance was not removed")
	}
	if claimed.Status != StatusIdle {
		t.Fatalf("status = %q, want %q", claimed.Status, StatusIdle)
	}
	var sawAssistant bool
	for _, msg := range claimed.Messages {
		if msg.Role == RoleAssistant && msg.Text == "Amanhã será quinta-feira." {
			sawAssistant = true
		}
	}
	if !sawAssistant {
		t.Fatalf("external assistant message was not merged into internal chat")
	}
}

func TestSetProviderMergesCurrentProviderExternalBeforeTransfer(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(t.TempDir(), "sandbox")
	internal := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: cwd})
	external := store.RegisterExternal(RegisterExternalInput{
		ProviderID:        "codex",
		CWD:               cwd,
		ProviderSessionID: "codex-run",
		TranscriptPath:    filepath.Join(t.TempDir(), "codex.jsonl"),
	})
	if _, appended, ok := store.AppendMessage(external.ID, AppendInput{Role: RoleAssistant, Text: "De nada.", SourceID: "codex.jsonl:1"}); !ok || !appended {
		t.Fatalf("expected external assistant append")
	}

	updated, ok := store.SetProvider(internal.ID, "gemini")
	if !ok {
		t.Fatalf("SetProvider failed")
	}
	if updated.ProviderID != "gemini" {
		t.Fatalf("provider = %q, want gemini", updated.ProviderID)
	}
	if _, ok := store.Get(external.ID); ok {
		t.Fatalf("duplicate external instance was not removed")
	}
	var sawAssistant bool
	for _, msg := range updated.Messages {
		if msg.Role == RoleAssistant && msg.Text == "De nada." {
			sawAssistant = true
		}
	}
	if !sawAssistant {
		t.Fatalf("external assistant message was not merged before transfer")
	}
}

func TestNewStoreMergesLoadedExternalWithSandboxIDOnlyCWD(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	store, err := NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	sandboxID := "c733ed4b2bf1e9bff18b41d977f1e244"
	cwd := filepath.Join(t.TempDir(), ".clichat", "sandbox", sandboxID)
	internal := store.CreateInternal(CreateInternalInput{ProviderID: "gemini", CWD: cwd})
	external := store.RegisterExternal(RegisterExternalInput{
		ProviderID:     "gemini",
		CWD:            sandboxID,
		TranscriptPath: filepath.Join(t.TempDir(), "session.jsonl"),
	})
	if _, appended, ok := store.AppendMessage(external.ID, AppendInput{Role: RoleAssistant, Text: "Gemini answered.", SourceID: "session.jsonl:1"}); !ok || !appended {
		t.Fatalf("expected external assistant append")
	}

	reloaded, err := NewStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(external.ID); ok {
		t.Fatalf("duplicate external instance was not removed on load")
	}
	got, ok := reloaded.Get(internal.ID)
	if !ok {
		t.Fatalf("internal instance missing after reload")
	}
	var sawAssistant bool
	for _, msg := range got.Messages {
		if msg.Role == RoleAssistant && msg.Text == "Gemini answered." {
			sawAssistant = true
		}
	}
	if !sawAssistant {
		t.Fatalf("loaded external Gemini message was not merged into internal chat")
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

func TestAppendMessageDuplicateAssistantClearsBusyStatus(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	inst := store.CreateInternal(CreateInternalInput{ProviderID: "codex", CWD: t.TempDir()})
	if _, appended, ok := store.AppendMessage(inst.ID, AppendInput{Role: RoleAssistant, Text: "Done", SourceID: "assistant:1"}); !ok || !appended {
		t.Fatalf("expected first assistant append")
	}
	if _, ok := store.SetStatus(inst.ID, StatusInput{Status: StatusBusy, Tool: "shell"}); !ok {
		t.Fatalf("expected busy status")
	}
	got, appended, ok := store.AppendMessage(inst.ID, AppendInput{Role: RoleAssistant, Text: "Done", SourceID: "assistant:1"})
	if !ok {
		t.Fatalf("duplicate append failed")
	}
	if appended {
		t.Fatalf("duplicate assistant should not append a second message")
	}
	if got.Status != StatusIdle {
		t.Fatalf("status = %q, want %q", got.Status, StatusIdle)
	}
	if got.CurrentTool != "" {
		t.Fatalf("current tool = %q, want empty", got.CurrentTool)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(got.Messages))
	}
}
