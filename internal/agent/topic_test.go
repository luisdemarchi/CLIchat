package agent

import (
	"strings"
	"testing"
)

func TestTopicFromTextKeepsProjectAndIntent(t *testing.T) {
	topic := TopicFromText("/Users/luis/projetos/pessoal/CLIchat this project needs a full refactor", "")
	if !strings.Contains(topic, "CLIchat") || !strings.Contains(strings.ToLower(topic), "project") {
		t.Fatalf("unexpected topic: %q", topic)
	}
}

func TestSmartTopicUsesLatestUserIntent(t *testing.T) {
	topic := SmartTopic([]Message{
		{Role: RoleUser, Text: "fix login"},
		{Role: RoleAssistant, Text: "ok"},
		{Role: RoleUser, Text: "make the chat title update like claude-mem"},
	}, "Login")
	if !strings.Contains(strings.ToLower(topic), "title") || !strings.Contains(strings.ToLower(topic), "claude-mem") {
		t.Fatalf("unexpected topic: %q", topic)
	}
}

func TestTopicFromAttachmentFallsBack(t *testing.T) {
	topic := TopicFromText("Attaching 2 files:\n- /tmp/a.png\n- /tmp/b.png", "Current summary")
	if topic != "Current summary" {
		t.Fatalf("expected fallback, got %q", topic)
	}
}
