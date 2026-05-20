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
	"runtime"
	"runtime/debug"
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
	case "install":
		if err := runInstall(false); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
	case "repair":
		if err := runInstall(true); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
	case "status", "doctor":
		if err := runStatus(); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
	case "logs":
		if err := runLogs(args); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
	case "uninstall":
		if err := runUninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
	case "attach":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "usage: clichat attach <session-id>")
			os.Exit(2)
		}
		attach(args[0])
	case "hook":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "usage: clichat hook <session-start|stop|pre-tool-use|post-tool-use>")
			os.Exit(2)
		}
		runHook(args[0])
	case "install-hooks":
		if err := installHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
			os.Exit(1)
		}
		fmt.Println("hooks installed in ~/.claude/settings.json")
	case "uninstall-hooks":
		if err := uninstallHooks(); err != nil {
			fmt.Fprintln(os.Stderr, "clichat:", err)
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
	fmt.Fprintln(os.Stderr, "usage: clichat <install|repair|status|logs|uninstall|attach|hook|list> ...")
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
		fmt.Fprintln(os.Stderr, "clichat:", err)
		os.Exit(1)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "ATTACH %s\n", sessionID); err != nil {
		fmt.Fprintln(os.Stderr, "clichat:", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "clichat:", err)
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
		fmt.Fprintln(os.Stderr, "clichat: unknown hook", name)
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
		fmt.Fprintln(os.Stderr, "clichat:", err)
		os.Exit(1)
	}
	fmt.Println(string(res))
}

func listInstances() {
	res, err := getJSON(httpBase()+"/v1/state", 2*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clichat:", err)
		os.Exit(1)
	}
	var parsed struct {
		Instances []map[string]any `json:"instances"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		fmt.Fprintln(os.Stderr, "clichat:", err)
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
		return "External Claude"
	}
	base := filepath.Base(cwd)
	if base == "." || base == "/" || base == "" {
		return "External Claude"
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

const (
	hookManagedTag    = "clichat-managed"
	legacyManagedTag  = "agentctl-managed"
	legacyAgentctlBin = "AGENTCTL_BIN"
)

func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func resolveClichatBinary() (string, error) {
	if v := os.Getenv("CLICHAT_BIN"); v != "" {
		return v, nil
	}
	if v := os.Getenv(legacyAgentctlBin); v != "" {
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
	bin, err := resolveClichatBinary()
	if err != nil {
		return err
	}
	return installHooksWithBinary(bin)
}

func installHooksWithBinary(bin string) error {
	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
		"matcher":    "*",
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
			if isManagedHook(matcher["_managedBy"]) {
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
		if matcher, ok := item.(map[string]any); ok && isManagedHook(matcher["_managedBy"]) {
			continue
		}
		stripped = append(stripped, item)
	}
	return stripped
}

func isManagedHook(value any) bool {
	return value == hookManagedTag || value == legacyManagedTag
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

// ---------------------------------------------------------------------------
// One-command installation
// ---------------------------------------------------------------------------

func runInstall(repair bool) error {
	if err := needCommand("go"); err != nil {
		return err
	}
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	buildRoot, err := prepareBuildRoot(repoRoot)
	if err != nil {
		return err
	}
	binDir := getenvDefault("CLICHAT_BIN_DIR", filepath.Join(mustHome(), ".local", "bin"))
	stateDir := filepath.Join(mustHome(), ".clichat")
	logDir := filepath.Join(stateDir, "logs")
	if err := needCommand("sqlite3"); err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}

	action := "install"
	if repair {
		action = "repair"
	}
	info("%s: building CLIchat binaries", action)
	hostBin := filepath.Join(binDir, "clichat-host")
	cliBin := filepath.Join(binDir, "clichat")
	_ = os.Remove(hostBin)
	_ = os.Remove(cliBin)
	if err := runCmd(buildRoot, "go", "build", "-o", hostBin, "./cmd/clichat-host"); err != nil {
		return err
	}
	if err := runCmd(buildRoot, "go", "build", "-o", cliBin, "./cmd/clichat"); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(binDir, "agentctl"))

	info("installing Claude Code hooks")
	if err := installHooksWithBinary(cliBin); err != nil {
		return err
	}

	if os.Getenv("SKIP_WAILS") != "1" {
		info("building desktop app")
		if err := ensureWails(); err != nil {
			return err
		}
		if err := buildWails(buildRoot); err != nil {
			return err
		}
		if err := placeApp(buildRoot); err != nil {
			warn("%v", err)
		}
	}

	info("installing background service")
	if err := installService(hostBin, logDir); err != nil {
		warn("%v", err)
	}

	fmt.Printf(`
CLIchat ready.

  Command:  %s
  Host:     %s
  State:    %s
  Memory:   %s
  Logs:     %s

Use:
  clichat status
  clichat logs
  clichat repair

`, cliBin, hostBin, filepath.Join(stateDir, "state.json"), filepath.Join(stateDir, "memory.sqlite3"), logDir)
	return nil
}

func runStatus() error {
	stateDir := filepath.Join(mustHome(), ".clichat")
	fmt.Println("CLIchat status")
	if _, err := getJSON(defaultHTTPBaseURL+"/health", 800*time.Millisecond); err == nil {
		fmt.Println("  host:   online")
	} else {
		fmt.Println("  host:   offline")
	}
	if data, err := getJSON(defaultHTTPBaseURL+"/v1/state", 800*time.Millisecond); err == nil {
		var parsed struct {
			Instances []any `json:"instances"`
		}
		_ = json.Unmarshal(data, &parsed)
		fmt.Printf("  chats:  %d\n", len(parsed.Instances))
	}
	printFileStatus("  state:  ", filepath.Join(stateDir, "state.json"))
	printFileStatus("  memory: ", filepath.Join(stateDir, "memory.sqlite3"))
	if hasManagedHooks() {
		fmt.Println("  hooks:  installed")
	} else {
		fmt.Println("  hooks:  missing")
	}
	return nil
}

func runLogs(args []string) error {
	logDir := filepath.Join(mustHome(), ".clichat", "logs")
	path := filepath.Join(logDir, "host.out.log")
	if len(args) > 0 && args[0] == "err" {
		path = filepath.Join(logDir, "host.err.log")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("log not found: %s", path)
	}
	cmd := exec.Command("tail", "-n", "120", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runUninstall() error {
	_ = uninstallHooks()
	home := mustHome()
	switch runtime.GOOS {
	case "darwin":
		plist := filepath.Join(home, "Library", "LaunchAgents", "com.clichat.host.plist")
		_ = exec.Command("launchctl", "unload", plist).Run()
		_ = os.Remove(plist)
		_ = os.RemoveAll("/Applications/CLIchat.app")
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "clichat.service").Run()
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", "clichat.service"))
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		_ = os.Remove(filepath.Join(home, ".local", "share", "clichat", "CLIchat"))
	}
	binDir := getenvDefault("CLICHAT_BIN_DIR", filepath.Join(home, ".local", "bin"))
	_ = os.Remove(filepath.Join(binDir, "clichat-host"))
	_ = os.Remove(filepath.Join(binDir, "agent-host"))
	_ = os.Remove(filepath.Join(binDir, "agentctl"))
	_ = os.Remove(filepath.Join(binDir, "clichat"))
	if os.Getenv("KEEP_STATE") == "0" {
		_ = os.RemoveAll(filepath.Join(home, ".clichat"))
		fmt.Println("state removed")
	} else {
		fmt.Println("state preserved in ~/.clichat (set KEEP_STATE=0 to remove)")
	}
	return nil
}

func findRepoRoot() (string, error) {
	if root, ok := findRepoRootFromWD(); ok {
		return root, nil
	}
	root, err := findRepoRootFromGoModule()
	if err == nil {
		return root, nil
	}
	return "", fmt.Errorf("could not locate CLIchat source: %w", err)
}

func prepareBuildRoot(source string) (string, error) {
	if isWritableDir(source) {
		return source, nil
	}
	dest := filepath.Join(mustHome(), ".clichat", "source")
	info("copying read-only module source to %s", dest)
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	if err := copyDir(source, dest); err != nil {
		return "", err
	}
	if err := makeTreeWritable(dest); err != nil {
		return "", err
	}
	return dest, nil
}

func isWritableDir(dir string) bool {
	file, err := os.CreateTemp(dir, ".clichat-write-test-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return true
}

func makeTreeWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		if entry.IsDir() {
			mode |= 0o700
		} else {
			mode |= 0o600
		}
		return os.Chmod(path, mode)
	})
}

func findRepoRootFromWD() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		data, err := os.ReadFile(filepath.Join(wd, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/luisdemarchi/CLIchat") {
			return wd, true
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return "", false
}

func findRepoRootFromGoModule() (string, error) {
	version := moduleVersion()
	if version == "" || version == "(devel)" {
		version = "latest"
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/luisdemarchi/CLIchat@"+version).Output()
	if err != nil && version != "latest" {
		out, err = exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/luisdemarchi/CLIchat@latest").Output()
	}
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errorsNew("go list returned an empty module directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", err
	}
	return dir, nil
}

func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Path == "github.com/luisdemarchi/CLIchat" {
			return info.Main.Version
		}
		for _, dep := range info.Deps {
			if dep.Path == "github.com/luisdemarchi/CLIchat" {
				return dep.Version
			}
		}
	}
	return ""
}

func ensureWails() error {
	if _, err := exec.LookPath("wails"); err == nil {
		return nil
	}
	info("Wails CLI not found; installing it with go install")
	if err := runCmd("", "go", "install", "github.com/wailsapp/wails/v2/cmd/wails@v2.12.0"); err != nil {
		return err
	}
	if _, err := resolveWails(); err != nil {
		return err
	}
	return nil
}

func buildWails(repoRoot string) error {
	wails, err := resolveWails()
	if err != nil {
		return err
	}
	if err := runCmd(repoRoot, wails, "build", "-clean", "-tags", "webkit2_41"); err == nil {
		return nil
	}
	return runCmd(repoRoot, wails, "build", "-clean")
}

func resolveWails() (string, error) {
	if path, err := exec.LookPath("wails"); err == nil {
		return path, nil
	}
	candidate := filepath.Join(mustHome(), "go", "bin", "wails")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", errorsNew("wails CLI not found")
}

func placeApp(repoRoot string) error {
	switch runtime.GOOS {
	case "darwin":
		src := filepath.Join(repoRoot, "build", "bin", "CLIchat.app")
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("desktop app not built at %s", src)
		}
		dst := "/Applications/CLIchat.app"
		_ = os.RemoveAll(dst)
		return copyDir(src, dst)
	case "linux":
		src := filepath.Join(repoRoot, "build", "bin", "CLIchat")
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("desktop app not built at %s", src)
		}
		dst := filepath.Join(mustHome(), ".local", "share", "clichat", "CLIchat")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyFile(src, dst, 0o755)
	default:
		return nil
	}
}

func installService(hostBin string, logDir string) error {
	switch runtime.GOOS {
	case "darwin":
		plist := filepath.Join(mustHome(), "Library", "LaunchAgents", "com.clichat.host.plist")
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			return err
		}
		content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.clichat.host</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + hostBin + `</string>
    <string>serve</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>` + filepath.Join(logDir, "host.out.log") + `</string>
  <key>StandardErrorPath</key>
  <string>` + filepath.Join(logDir, "host.err.log") + `</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
  </dict>
</dict>
</plist>
`
		if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
			return err
		}
		_ = exec.Command("launchctl", "unload", plist).Run()
		return exec.Command("launchctl", "load", plist).Run()
	case "linux":
		service := filepath.Join(mustHome(), ".config", "systemd", "user", "clichat.service")
		if err := os.MkdirAll(filepath.Dir(service), 0o755); err != nil {
			return err
		}
		content := `[Unit]
Description=CLIchat host
After=network.target

[Service]
ExecStart=` + hostBin + ` serve
Restart=always
RestartSec=2
StandardOutput=append:` + filepath.Join(logDir, "host.out.log") + `
StandardError=append:` + filepath.Join(logDir, "host.err.log") + `

[Install]
WantedBy=default.target
`
		if err := os.WriteFile(service, []byte(content), 0o644); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		return exec.Command("systemctl", "--user", "enable", "--now", "clichat.service").Run()
	default:
		return nil
	}
}

func needCommand(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not found", name)
	}
	return nil
}

func runCmd(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyDir(src, dst string) error {
	return runCmd("", "cp", "-R", src, dst)
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func printFileStatus(label, path string) {
	if _, err := os.Stat(path); err == nil {
		fmt.Println(label + path)
	} else {
		fmt.Println(label + "missing")
	}
}

func hasManagedHooks() bool {
	path, err := claudeSettingsPath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte(hookManagedTag))
}

func info(format string, args ...any) {
	fmt.Printf("==> "+format+"\n", args...)
}

func warn(format string, args ...any) {
	fmt.Printf("!! "+format+"\n", args...)
}

func getenvDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func mustHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func errorsNew(message string) error {
	return fmt.Errorf("%s", message)
}
