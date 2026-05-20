package provider

import (
	"os"
	"os/exec"
	"path/filepath"
)

type ID string

const (
	Claude ID = "claude"
	Gemini ID = "gemini"
	Codex  ID = "codex"
)

type Provider struct {
	ID          ID       `json:"id"`
	Name        string   `json:"name"`
	CLI         string   `json:"cli"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Tag         string   `json:"tag"`
	Accent      string   `json:"accent"`
	Available   bool     `json:"available"`
	Description string   `json:"description"`
}

func Defaults() []Provider {
	providers := []Provider{
		{
			ID:          Claude,
			Name:        "Claude",
			CLI:         "claude",
			Tag:         "CLAUDE",
			Accent:      "#6f5adc",
			Description: "Claude Code in an app-managed local session.",
		},
		{
			ID:          Codex,
			Name:        "Codex",
			CLI:         "codex",
			Args:        []string{"--no-alt-screen", "--dangerously-bypass-hook-trust"},
			Tag:         "CODEX",
			Accent:      "#a45f18",
			Description: "Codex CLI in an app-managed local session.",
		},
		{
			ID:          Gemini,
			Name:        "Gemini",
			CLI:         "gemini",
			Args:        []string{"--screen-reader", "--skip-trust"},
			Tag:         "GEMINI",
			Accent:      "#167c80",
			Description: "Gemini CLI in an app-managed local session.",
		},
	}

	for index := range providers {
		command, ok := findCommand(providers[index].CLI)
		providers[index].Command = command
		providers[index].Available = ok
	}

	return providers
}

func findCommand(name string) (string, bool) {
	if path, err := exec.LookPath(name); err == nil {
		return path, true
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, "bin", name),
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
		"/usr/bin/" + name,
		"/bin/" + name,
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return candidate, true
		}
	}

	return name, false
}

func ByID(id ID) (Provider, bool) {
	for _, candidate := range Defaults() {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Provider{}, false
}
