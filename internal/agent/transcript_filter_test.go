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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSystemTranscriptEntry(tc.text); got != tc.want {
				t.Errorf("isSystemTranscriptEntry(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
