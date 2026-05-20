package agent

import (
	"regexp"
	"strings"
	"sync"
)

const promptBufferLimit = 8 * 1024

var (
	ansiPromptPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)
	controlPattern    = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
	// numberedOption (relaxed): matches "1. label" or "  1. label" with the TUI
	// cursor glyph (❯ › >) optional. Claude's TUI only marks the currently
	// selected option with the glyph, so requiring it on every line drops the
	// non-selected siblings. We still gate menu detection via cursorOption /
	// menuTriggerPhrase to avoid catching ordinary numbered lists in replies.
	numberedOption = regexp.MustCompile(`(?m)^\s*[❯›>]?\s*([1-9])[.\)]\s+(.{1,80})$`)
	// cursorOption: at least one option must have the cursor glyph or the
	// trigger phrase must be present — proves the buffer is a TUI menu and
	// not a numbered list inside a normal markdown answer.
	cursorOption = regexp.MustCompile(`(?m)^\s*[❯›>]\s+[1-9][.\)]\s+`)
	// triggerPhrase: textual hints that the TUI is asking for a choice.
	menuTriggerPhrase = regexp.MustCompile(`(?i)\b(choose|select|pick|escolha|selecione|qual\s+(opção|opcao)|press\s+\d|enter\s+\d|what\s+do\s+you\s+want\s+to\s+do|what\s+would\s+you\s+like\s+to\s+do|enter\s+to\s+confirm|esc\s+to\s+cancel)\b`)
)

// PromptDetector keeps a sliding buffer of cleaned terminal output per session
// and tries to recognize "select an option" style menus or yes/no confirmations
// that the Claude / Codex / Gemini TUIs ask. When it finds one, it returns a
// question + actions list ready for store.SetPending.
type PromptDetector struct {
	mu      sync.Mutex
	buffers map[string]string
	last    map[string]string // last fingerprint emitted, used to dedup
}

func NewPromptDetector() *PromptDetector {
	return &PromptDetector{
		buffers: make(map[string]string),
		last:    make(map[string]string),
	}
}

// Feed appends a chunk and returns (question, actions, true) if a prompt was
// detected. Caller is expected to forward the result to the store.
func (d *PromptDetector) Feed(sessionID string, chunk []byte) (string, []PendingAction, bool) {
	clean := stripAnsi(string(chunk))
	if clean == "" {
		return "", nil, false
	}

	d.mu.Lock()
	buf := d.buffers[sessionID] + clean
	if len(buf) > promptBufferLimit {
		buf = buf[len(buf)-promptBufferLimit:]
	}
	d.buffers[sessionID] = buf
	prev := d.last[sessionID]
	d.mu.Unlock()

	question, actions, ok := DetectPrompt(buf)
	if !ok {
		return "", nil, false
	}

	fingerprint := question + "|"
	for _, a := range actions {
		fingerprint += a.Label + "/"
	}
	if fingerprint == prev {
		return "", nil, false
	}

	d.mu.Lock()
	d.last[sessionID] = fingerprint
	// reset the buffer once we've consumed it so the next prompt starts fresh
	d.buffers[sessionID] = ""
	d.mu.Unlock()
	return question, actions, true
}

// Clear forgets state for a session (call on instance removal).
func (d *PromptDetector) Clear(sessionID string) {
	d.mu.Lock()
	delete(d.buffers, sessionID)
	delete(d.last, sessionID)
	d.mu.Unlock()
}

func stripAnsi(text string) string {
	text = ansiPromptPattern.ReplaceAllString(text, "")
	text = controlPattern.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func DetectPrompt(buffer string) (string, []PendingAction, bool) {
	// Treat numbered lines as a menu when EITHER a trigger phrase is present
	// OR at least one option has the TUI cursor glyph (❯ › >). Claude's TUI
	// marks only the selected option with the glyph, so we cannot require it
	// on every line. The combined gate (trigger OR any-cursor) keeps regular
	// numbered lists inside markdown answers from being misclassified.
	if menuTriggerPhrase.MatchString(buffer) || cursorOption.MatchString(buffer) {
		matches := numberedOption.FindAllStringSubmatch(buffer, -1)
		options := make([]PendingAction, 0, len(matches))
		seen := make(map[string]bool)
		for _, match := range matches {
			number := match[1]
			label := strings.TrimSpace(match[2])
			if label == "" || seen[number] {
				continue
			}
			seen[number] = true
			options = append(options, PendingAction{ID: number, Label: number + ". " + label, Input: number})
		}
		if len(options) >= 2 {
			question := promptQuestionFromBuffer(buffer)
			return question, options, true
		}
	}

	lower := strings.ToLower(buffer)
	yesNoTriggers := []string{
		"do you trust",
		"trust this folder",
		"trust the authors",
		"do you want to proceed",
		"do you want to continue",
		"do you want to allow",
		"allow this command",
		"approve this",
		"are you sure",
		"continue?",
		"proceed?",
		"(y/n)",
		"[y/n]",
		"yes/no",
		"confirm?",
	}
	for _, trigger := range yesNoTriggers {
		if strings.Contains(lower, trigger) {
			return promptQuestionFromBuffer(buffer), []PendingAction{
				{ID: "yes", Label: "Yes", Input: "y"},
				{ID: "no", Label: "No", Input: "n"},
			}, true
		}
	}
	continueTriggers := []string{
		"press enter to continue",
		"pressione enter para continuar",
		"aperte enter para continuar",
		"hit enter to continue",
		"return to continue",
	}
	for _, trigger := range continueTriggers {
		if strings.Contains(lower, trigger) {
			return promptQuestionFromBuffer(buffer), []PendingAction{
				{ID: "continue", Label: "Continuar", Input: ""},
			}, true
		}
	}
	return "", nil, false
}

func promptQuestionFromBuffer(buffer string) string {
	lines := strings.Split(buffer, "\n")
	kept := make([]string, 0, 4)
	for i := len(lines) - 1; i >= 0 && len(kept) < 4; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		// skip option lines
		if numberedOption.MatchString(line) {
			continue
		}
		// skip pure decoration
		if strings.Trim(line, "─━═-= │┃|.•·") == "" {
			continue
		}
		kept = append([]string{line}, kept...)
	}
	question := strings.Join(kept, " ")
	if question == "" {
		question = "The CLI is waiting for a choice."
	}
	if len(question) > 280 {
		question = question[:280] + "…"
	}
	return question
}
