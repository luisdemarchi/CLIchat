package agent

import "testing"

func TestDetectPromptClaudeRateLimitMenu(t *testing.T) {
	// Real menu rendered by Claude's TUI when the user hits the rate limit.
	// Only the selected option is prefixed with the cursor (>); siblings have
	// only indentation. The original detector required the cursor on every
	// option and missed this case.
	buffer := `What do you want to do?

> 1. Stop and wait for limit to reset
  2. Upgrade your plan
  3. Upgrade to Team plan

Enter to confirm · Esc to cancel`

	question, actions, ok := DetectPrompt(buffer)
	if !ok {
		t.Fatalf("expected menu detection, got ok=false")
	}
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d: %+v", len(actions), actions)
	}
	want := []string{"1", "2", "3"}
	for i, a := range actions {
		if a.ID != want[i] {
			t.Errorf("action[%d].ID = %q, want %q", i, a.ID, want[i])
		}
		if a.Input != want[i] {
			t.Errorf("action[%d].Input = %q, want %q", i, a.Input, want[i])
		}
	}
	if question == "" {
		t.Errorf("expected non-empty question")
	}
}

func TestDetectPromptIgnoresMarkdownNumberedList(t *testing.T) {
	// Plain numbered list inside a normal assistant answer — no cursor, no
	// trigger phrase. Must NOT be classified as a menu.
	buffer := `Resumo das mudanças aplicadas:

1. Refatorei o módulo de auth
2. Adicionei testes
3. Atualizei a documentação

Tudo pronto pra revisar.`

	if _, _, ok := DetectPrompt(buffer); ok {
		t.Fatalf("did not expect menu detection on markdown list")
	}
}

func TestDetectPromptTriggerPhraseNoCursor(t *testing.T) {
	// Trigger phrase present without any cursor glyph (e.g. plain CLI).
	buffer := `Choose an option:

1. First
2. Second
3. Third`

	_, actions, ok := DetectPrompt(buffer)
	if !ok {
		t.Fatalf("expected menu detection with trigger phrase")
	}
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}
}

func TestDetectPromptYesNoTrustFolder(t *testing.T) {
	buffer := `Do you trust this folder? (y/n)`
	_, actions, ok := DetectPrompt(buffer)
	if !ok {
		t.Fatalf("expected y/n detection")
	}
	if len(actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(actions))
	}
	if actions[0].Input != "y" || actions[1].Input != "n" {
		t.Errorf("expected y/n inputs, got %q/%q", actions[0].Input, actions[1].Input)
	}
}

func TestDetectPromptCursorOnlyNoTrigger(t *testing.T) {
	// Cursor present on selected option, no trigger phrase. Should detect.
	buffer := `Pick a model:

❯ 1. claude-opus
  2. claude-sonnet
  3. claude-haiku`

	_, actions, ok := DetectPrompt(buffer)
	if !ok {
		t.Fatalf("expected menu detection with cursor only")
	}
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}
}