package provider

import "os/exec"

type ID string

const (
	Claude ID = "claude"
	Gemini ID = "gemini"
	Codex  ID = "codex"
)

type Provider struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	CLI         string `json:"cli"`
	Tag         string `json:"tag"`
	Accent      string `json:"accent"`
	Available   bool   `json:"available"`
	Description string `json:"description"`
}

func Defaults() []Provider {
	providers := []Provider{
		{
			ID:          Claude,
			Name:        "Claude",
			CLI:         "claude",
			Tag:         "CLAUDE",
			Accent:      "#6f5adc",
			Description: "Claude Code em uma sessao local controlada pelo app.",
		},
		{
			ID:          Gemini,
			Name:        "Gemini",
			CLI:         "gemini",
			Tag:         "GEMINI",
			Accent:      "#167c80",
			Description: "Gemini CLI em uma sessao local controlada pelo app.",
		},
		{
			ID:          Codex,
			Name:        "Codex",
			CLI:         "codex",
			Tag:         "CODEX",
			Accent:      "#a45f18",
			Description: "Codex CLI em uma sessao local controlada pelo app.",
		},
	}

	for index := range providers {
		_, err := exec.LookPath(providers[index].CLI)
		providers[index].Available = err == nil
	}

	return providers
}

func ByID(id ID) (Provider, bool) {
	for _, candidate := range Defaults() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Provider{}, false
}
