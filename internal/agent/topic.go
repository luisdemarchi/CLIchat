package agent

import (
	"path/filepath"
	"strings"
	"unicode"
)

const maxTopicChars = 64

var topicStopwords = map[string]struct{}{
	"a": {}, "agora": {}, "ainda": {}, "ai": {}, "ao": {}, "aos": {}, "as": {}, "até": {},
	"cada": {}, "com": {}, "como": {}, "da": {}, "das": {}, "de": {}, "dei": {}, "dentro": {},
	"do": {}, "dos": {}, "e": {}, "ela": {}, "ele": {}, "em": {}, "essa": {}, "esse": {},
	"esta": {}, "está": {}, "estao": {}, "estão": {}, "eu": {}, "fazer": {}, "foi": {},
	"garanta": {}, "isso": {}, "ja": {}, "já": {}, "la": {}, "lá": {}, "mas": {},
	"me": {}, "mesmo": {}, "na": {}, "nas": {}, "nao": {}, "não": {}, "nem": {}, "no": {},
	"nos": {}, "o": {}, "os": {}, "ou": {}, "para": {}, "pela": {}, "pelo": {}, "pra": {},
	"precisa": {}, "preciso": {}, "que": {}, "quero": {}, "se": {}, "seja": {}, "sem": {},
	"ser": {}, "sobre": {}, "ta": {}, "tá": {}, "talvez": {}, "te": {}, "tem": {}, "tenha": {},
	"ter": {}, "todo": {}, "todos": {}, "um": {}, "uma": {}, "vc": {}, "ver": {}, "veja": {},
	"you": {}, "the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "this": {}, "that": {},
}

// SmartTopic derives the current user-facing topic for a chat from the latest
// user intent. It is intentionally local and deterministic: the UI must keep a
// clean title even when the provider does not call agent_chat_set_topic.
func SmartTopic(messages []Message, fallback string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != RoleUser {
			continue
		}
		if topic := TopicFromText(msg.Text, fallback); topic != "" {
			return topic
		}
	}
	return trimTopic(fallback)
}

func TopicFromText(text string, fallback string) string {
	cleaned := cleanTopicText(text)
	if cleaned == "" {
		return trimTopic(fallback)
	}
	tokens := topicTokens(cleaned)
	if len(tokens) == 0 {
		return trimTopic(fallback)
	}

	kept := make([]string, 0, 7)
	for _, token := range tokens {
		key := strings.ToLower(token)
		if _, stop := topicStopwords[key]; stop {
			continue
		}
		if len([]rune(token)) < 3 && !hasDigit(token) && !isLikelyIdentifier(token) {
			continue
		}
		kept = append(kept, token)
		if len(kept) == 7 {
			break
		}
	}
	if len(kept) == 0 {
		for _, token := range tokens {
			kept = append(kept, token)
			if len(kept) == 5 {
				break
			}
		}
	}
	return titleizeTopic(strings.Join(kept, " "))
}

func cleanTopicText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.Contains(text, "Attaching ") && strings.Contains(text, "file") {
		return ""
	}
	text = collapsePaths(text)

	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		lines = append(lines, line)
		if len(strings.Join(lines, " ")) > 280 {
			break
		}
	}
	text = strings.Join(lines, " ")
	text = strings.ReplaceAll(text, "!!!", " ")
	text = strings.ReplaceAll(text, "???", " ")
	text = strings.ReplaceAll(text, ":", " ")
	text = strings.ReplaceAll(text, ";", " ")
	text = strings.Join(strings.Fields(text), " ")
	if idx := strings.IndexAny(text, ".!?"); idx > 40 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

func collapsePaths(text string) string {
	fields := strings.Fields(text)
	for i, field := range fields {
		trimmed := strings.Trim(field, " \t\r\n.,;:!?()[]{}")
		if !strings.HasPrefix(trimmed, "/") && !strings.HasPrefix(trimmed, "~/") {
			continue
		}
		base := filepath.Base(trimmed)
		if base == "." || base == "/" || base == "" {
			continue
		}
		fields[i] = strings.Replace(field, trimmed, base, 1)
	}
	return strings.Join(fields, " ")
}

func topicTokens(text string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '#' || r == '.':
			if b.Len() > 0 {
				b.WriteRune(r)
			}
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func titleizeTopic(topic string) string {
	topic = trimTopic(topic)
	if topic == "" {
		return ""
	}
	runes := []rune(topic)
	for i, r := range runes {
		if unicode.IsLetter(r) {
			runes[i] = unicode.ToUpper(r)
			break
		}
	}
	return string(runes)
}

func trimTopic(topic string) string {
	topic = strings.Trim(strings.Join(strings.Fields(topic), " "), " \t\r\n-.,;:!?")
	if topic == "" {
		return ""
	}
	runes := []rune(topic)
	if len(runes) <= maxTopicChars {
		return topic
	}
	return strings.TrimSpace(string(runes[:maxTopicChars-3])) + "..."
}

func hasDigit(value string) bool {
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func isLikelyIdentifier(value string) bool {
	return strings.ContainsAny(value, "-_#.") || value != strings.ToLower(value)
}
