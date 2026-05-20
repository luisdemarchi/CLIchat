package agent

import "testing"

func TestIsSystemTranscriptEntry(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "task-notification block",
			text: `<task-notification>
<task-id>bnif57dam</task-id>
<status>completed</status>
</task-notification>`,
			want: true,
		},
		{
			name: "system-reminder",
			text: "<system-reminder>\nUserPromptSubmit hook\n</system-reminder>",
			want: true,
		},
		{
			name: "command-name slash",
			text: "<command-name>/model</command-name>\n<command-message>switch</command-message>",
			want: true,
		},
		{
			name: "leading whitespace",
			text: "   \n  <task-notification>\n<status>ok</status>\n</task-notification>",
			want: true,
		},
		{
			name: "plain user message",
			text: "oi, como vai?",
			want: false,
		},
		{
			name: "user message starting with HTML angle bracket inside markdown",
			text: "Compare: x < y < z. Use the snippet:",
			want: false,
		},
		{
			name: "code block not flagged",
			text: "```html\n<div>example</div>\n```",
			want: false,
		},
		{
			name: "bash-input",
			text: "<bash-input>ls -la</bash-input>",
			want: true,
		},
		{
			name: "codex environment context",
			text: "<environment_context>\n  <cwd>/tmp</cwd>\n</environment_context>",
			want: true,
		},
		{
			name: "internal clichat handoff",
			text: "<clichat-handoff>\nInternal memory\n</clichat-handoff>",
			want: true,
		},
		{
			name: "codex AGENTS boot context",
			text: "# AGENTS.md instructions for /tmp/project\n\n<INSTRUCTIONS>\ninternal context\n</INSTRUCTIONS>",
			want: true,
		},
		{
			name: "bare instructions boot context",
			text: "<INSTRUCTIONS>\ninternal context\n</INSTRUCTIONS>",
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSystemTranscriptEntry(tc.text); got != tc.want {
				t.Errorf("isSystemTranscriptEntry(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestParseCodexLineConversationOnly(t *testing.T) {
	userLine := []byte(`{"type":"event_msg","payload":{"type":"user_message","message":"oi"}}`)
	entry, ok := parseCodexLine(userLine)
	if !ok {
		t.Fatalf("expected user event to parse")
	}
	if entry.Role != RoleUser || entry.Text != "oi" {
		t.Fatalf("user entry = role %q text %q", entry.Role, entry.Text)
	}

	assistantLine := []byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Ola!"}]}}`)
	entry, ok = parseCodexLine(assistantLine)
	if !ok {
		t.Fatalf("expected assistant message to parse")
	}
	if entry.Role != RoleAssistant || entry.Text != "Ola!" {
		t.Fatalf("assistant entry = role %q text %q", entry.Role, entry.Text)
	}

	reasoningLine := []byte(`{"type":"event_msg","payload":{"type":"agent_message","message":"thinking..."}}`)
	if _, ok := parseCodexLine(reasoningLine); ok {
		t.Fatalf("agent_message should not become a chat bubble")
	}

	envLine := []byte(`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n</environment_context>"}]}}`)
	if _, ok := parseCodexLine(envLine); ok {
		t.Fatalf("environment context should not become a user bubble")
	}
}

func TestCodexSessionIDFromPath(t *testing.T) {
	path := "/Users/me/.codex/sessions/2026/05/20/rollout-2026-05-20T10-21-35-019e458c-7bf8-7272-8e4c-2f8b2a7d27fa.jsonl"
	got := codexSessionIDFromPath(path)
	want := "019e458c-7bf8-7272-8e4c-2f8b2a7d27fa"
	if got != want {
		t.Fatalf("codexSessionIDFromPath() = %q, want %q", got, want)
	}
}
