package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	defaultHTTPBaseURL = "http://127.0.0.1:47657"
	defaultTTYAddress  = "127.0.0.1:47656"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "attach":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: agentctl attach <session-id>")
			os.Exit(2)
		}
		attach(args[0])
	case "hook":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: agentctl hook <session-start|stop|pre-tool-use|post-tool-use>")
			os.Exit(2)
		}
		runHook(args[0])
	case "install-hooks":
		if err := installHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "agentctl:", err)
			os.Exit(1)
		}
		fmt.Println("hooks installed in ~/.claude/settings.json")
	case "uninstall-hooks":
		if err := uninstallHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "agentctl:", err)
			os.Exit(1)
		}
		fmt.Println("hooks removed from ~/.claude/settings.json")
	case "list":
		listInstances()
	case "register":
		registerCmd(args)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: agentctl <attach|hook|install-hooks|uninstall-hooks|list|register> ...")
}

func httpBase() string {
	if v := os.Getenv("AGENT_CHAT_HOST"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultHTTPBaseURL
}

func attach(sessionID string) {
	address := os.Getenv("AGENT_CHAT_TTY_ADDR")
	if address == "" {
		address = defaultTTYAddress
	}

	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "ATTACH %s\n", sessionID); err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	if len(line) < 2 || line[:2] != "OK" {
		fmt.Fprint(os.Stderr, line)
		os.Exit(1)
	}
	fmt.Fprint(os.Stderr, line)

	restore, err := makeRaw()
	if err == nil {
		defer restore()
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, reader)
		done <- struct{}{}
	}()
	<-done
}

func makeRaw() (func(), error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return func() {}, nil
	}
	previous, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() {
		_ = term.Restore(fd, previous)
	}, nil
}

type claudeHookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	Source         string `json:"source"`
	ToolName       string `json:"tool_name"`
}

func readHookStdin() claudeHookInput {
	var input claudeHookInput
	data, err := io.ReadAll(os.Stdin)
	if err != nil || len(data) == 0 {
		return input
	}
	_ = json.Unmarshal(data, &input)
	return input
}

func runHook(name string) {
	input := readHookStdin()
	switch strings.ToLower(name) {
	case "session-start":
		hookSessionStart(input)
	case "stop":
		hookStop(input)
	case "pre-tool-use":
		hookPreToolUse(input)
	case "post-tool-use":
		hookPostToolUse(input)
	case "user-prompt-submit":
		hookUserPromptSubmit(input)
	default:
		fmt.Fprintln(os.Stderr, "agentctl: unknown hook", name)
		os.Exit(2)
	}
}

func hookSessionStart(input claudeHookInput) {
	cwd := firstNonEmpty(input.CWD, mustGetwd())

	// If this Claude was spawned by the app, AGENT_CHAT_INTERNAL_SESSION_ID is set in env.
	// Attach Claude metadata to the existing internal instance instead of creating a duplicate external one.
	if internalID := strings.TrimSpace(os.Getenv("AGENT_CHAT_INTERNAL_SESSION_ID")); internalID != "" {
		body := map[string]any{
			"claudeSessionId": input.SessionID,
			"transcriptPath":  input.TranscriptPath,
			"cwd":             cwd,
			"pid":             os.Getppid(),
			"tty":             detectTTY(),
		}
		_, _ = postJSON(httpBase()+"/v1/instances/"+internalID+"/attach-claude", body, 1500*time.Millisecond)
		return
	}

	title := defaultExternalTitle(cwd)
	body := map[string]any{
		"origin":          "external",
		"providerId":      "claude",
		"title":           title,
		"cwd":             cwd,
		"claudeSessionId": input.SessionID,
		"transcriptPath":  input.TranscriptPath,
		"pid":             os.Getppid(),
		"tty":             detectTTY(),
	}
	_, _ = postJSON(httpBase()+"/v1/instances", body, 1500*time.Millisecond)
}

func resolveHookInstanceID(claudeSessionID string) string {
	if id := strings.TrimSpace(os.Getenv("AGENT_CHAT_INTERNAL_SESSION_ID")); id != "" {
		return id
	}
	return lookupInstanceID(claudeSessionID)
}

func hookStop(input claudeHookInput) {
	id := resolveHookInstanceID(input.SessionID)
	if id == "" {
		return
	}
	_, _ = postJSON(httpBase()+"/v1/instances/"+id+"/status", map[string]string{"status": "idle", "tool": ""}, 1500*time.Millisecond)
}

func hookPreToolUse(input claudeHookInput) {
	id := resolveHookInstanceID(input.SessionID)
	if id == "" {
		return
	}
	_, _ = postJSON(httpBase()+"/v1/instances/"+id+"/status", map[string]string{"status": "busy", "tool": input.ToolName}, 1500*time.Millisecond)
}

func hookPostToolUse(input claudeHookInput) {
	id := resolveHookInstanceID(input.SessionID)
	if id == "" {
		return
	}
	// Keep status busy between tool calls — only Stop releases. Clear tool name though,
	// so the avatar shows the generic "thinking" emoji until next tool fires.
	_, _ = postJSON(httpBase()+"/v1/instances/"+id+"/status", map[string]string{"status": "busy", "tool": ""}, 1500*time.Millisecond)
}

func hookUserPromptSubmit(input claudeHookInput) {
	id := resolveHookInstanceID(input.SessionID)
	if id == "" {
		return
	}
	_, _ = postJSON(httpBase()+"/v1/instances/"+id+"/status", map[string]string{"status": "busy", "tool": ""}, 1500*time.Millisecond)
}

type instancesResponse struct {
	Instances []struct {
		ID              string `json:"id"`
		ClaudeSessionID string `json:"claudeSessionId"`
	} `json:"instances"`
}

func lookupInstanceID(claudeSessionID string) string {
	if claudeSessionID == "" {
		return ""
	}
	res, err := getJSON(httpBase()+"/v1/state", 1000*time.Millisecond)
	if err != nil {
		return ""
	}
	var parsed instancesResponse
	if err := json.Unmarshal(res, &parsed); err != nil {
		return ""
	}
	for _, inst := range parsed.Instances {
		if inst.ClaudeSessionID == claudeSessionID {
			return inst.ID
		}
	}
	return ""
}

func registerCmd(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	title := fs.String("title", "", "Instance title")
	cwd := fs.String("cwd", mustGetwd(), "Working directory")
	claudeID := fs.String("claude-session-id", "", "Claude Code session UUID")
	transcript := fs.String("transcript-path", "", "Claude Code transcript path")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	body := map[string]any{
		"origin":          "external",
		"providerId":      "claude",
		"title":           *title,
		"cwd":             *cwd,
		"claudeSessionId": *claudeID,
		"transcriptPath":  *transcript,
		"pid":             os.Getppid(),
		"tty":             detectTTY(),
	}
	res, err := postJSON(httpBase()+"/v1/instances", body, 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	fmt.Println(string(res))
}

func listInstances() {
	res, err := getJSON(httpBase()+"/v1/state", 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	var parsed struct {
		Instances []map[string]any `json:"instances"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	for _, inst := range parsed.Instances {
		fmt.Printf("%-32s  %-9s  %-7s  %s\n", inst["id"], inst["origin"], inst["status"], inst["title"])
	}
}

func postJSON(url string, body any, timeout time.Duration) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return out, fmt.Errorf("HTTP %d: %s", res.StatusCode, string(out))
	}
	return out, nil
}

func getJSON(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	res, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(res.Body)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func detectTTY() string {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		if name, err := os.Readlink("/dev/stdin"); err == nil {
			return name
		}
	}
	// Hooks usually have stdin piped; fall back to looking up the controlling tty
	// of our parent process via `ps`.
	for _, pid := range []int{os.Getppid(), os.Getpid()} {
		out, err := exec.Command("ps", "-o", "tty=", "-p", fmt.Sprintf("%d", pid)).Output()
		if err != nil {
			continue
		}
		tty := strings.TrimSpace(string(out))
		if tty == "" || tty == "??" || tty == "?" || tty == "-" {
			continue
		}
		if !strings.HasPrefix(tty, "/dev/") {
			tty = "/dev/" + tty
		}
		return tty
	}
	return ""
}

func defaultExternalTitle(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "Claude externo"
	}
	base := filepath.Base(cwd)
	if base == "." || base == "/" || base == "" {
		return "Claude externo"
	}
	return "Claude · " + base
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Hook installation
// ---------------------------------------------------------------------------

const hookManagedTag = "agentctl-managed"

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func resolveAgentctlBinary() (string, error) {
	if v := os.Getenv("AGENTCTL_BIN"); v != "" {
		return v, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		return resolved, nil
	}
	return exe, nil
}

func installHooks() error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	bin, err := resolveAgentctlBinary()
	if err != nil {
		return err
	}

	settings := readJSONObject(path)

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	for _, event := range []string{"SessionStart", "Stop", "PreToolUse", "PostToolUse", "UserPromptSubmit"} {
		matchers := managedMatcher(event, bin)
		hooks[event] = mergeManagedMatchers(hooks[event], matchers)
	}
	settings["hooks"] = hooks
	return writeJSONObject(path, settings)
}

func uninstallHooks() error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}
	settings := readJSONObject(path)
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	for _, event := range []string{"SessionStart", "Stop", "PreToolUse", "PostToolUse", "UserPromptSubmit"} {
		hooks[event] = stripManagedMatchers(hooks[event])
		if list, ok := hooks[event].([]any); ok && len(list) == 0 {
			delete(hooks, event)
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return writeJSONObject(path, settings)
}

func managedMatcher(event string, bin string) map[string]any {
	hookName := ""
	switch event {
	case "SessionStart":
		hookName = "session-start"
	case "Stop":
		hookName = "stop"
	case "PreToolUse":
		hookName = "pre-tool-use"
	case "PostToolUse":
		hookName = "post-tool-use"
	case "UserPromptSubmit":
		hookName = "user-prompt-submit"
	default:
		hookName = strings.ToLower(event)
	}
	hookCommand := shellQuote(bin) + " hook " + hookName
	return map[string]any{
		"matcher": "*",
		"_managedBy": hookManagedTag,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": hookCommand,
				"timeout": 5,
			},
		},
	}
}

func mergeManagedMatchers(existing any, managed map[string]any) []any {
	list, _ := existing.([]any)
	merged := make([]any, 0, len(list)+1)
	for _, item := range list {
		if matcher, ok := item.(map[string]any); ok {
			if matcher["_managedBy"] == hookManagedTag {
				continue
			}
			merged = append(merged, matcher)
		} else {
			merged = append(merged, item)
		}
	}
	merged = append(merged, managed)
	return merged
}

func stripManagedMatchers(existing any) []any {
	list, _ := existing.([]any)
	if len(list) == 0 {
		return nil
	}
	stripped := make([]any, 0, len(list))
	for _, item := range list {
		if matcher, ok := item.(map[string]any); ok && matcher["_managedBy"] == hookManagedTag {
			continue
		}
		stripped = append(stripped, item)
	}
	return stripped
}

func readJSONObject(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func writeJSONObject(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func shellQuote(value string) string {
	if !strings.ContainsAny(value, " \t\"'\\$") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

