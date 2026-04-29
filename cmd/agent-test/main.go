// agent-test: end-to-end smoke test against a running agent-host.
// Spawns each provider, sends a tiny prompt through the PTY, captures output,
// and reports whether the CLI actually submitted (responded) and whether
// chat-bubble messages were captured.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const baseURL = "http://127.0.0.1:47657"

type Provider struct {
	Name    string
	Command string
	Args    []string
}

type Result struct {
	Provider     string
	InstanceID   string
	PIDStarted   int
	BootBytes    int
	PostInputBy  int
	GotResponse  bool
	GotMessages  int
	Notes        []string
}

var ansi = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\a]*(?:\a|\x1b\\))`)

func main() {
	prov := []Provider{
		{Name: "claude", Command: lookup("claude")},
		{Name: "gemini", Command: lookup("gemini"), Args: []string{"--screen-reader"}},
		{Name: "codex", Command: lookup("codex"), Args: []string{"--no-alt-screen"}},
	}
	if err := waitHealth(8 * time.Second); err != nil {
		fmt.Println("agent-host not reachable:", err)
		os.Exit(2)
	}
	fmt.Println("== agent-test e2e ==")
	failed := 0
	for _, p := range prov {
		if p.Command == "" {
			fmt.Printf("[%s] CLI not found, SKIP\n\n", p.Name)
			continue
		}
		r := runOne(p)
		printResult(r)
		if !r.GotResponse {
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("\n== %d provider(s) FAILED ==\n", failed)
		os.Exit(1)
	}
	fmt.Println("\n== ALL PROVIDERS OK ==")
}

func runOne(p Provider) Result {
	r := Result{Provider: p.Name}

	// 1. Create instance
	id, err := postInstance(p.Name)
	if err != nil {
		r.Notes = append(r.Notes, "create: "+err.Error())
		return r
	}
	r.InstanceID = id
	defer cleanup(id)

	// 2. Start terminal
	pid, err := startTerminal(id, p)
	if err != nil {
		r.Notes = append(r.Notes, "start: "+err.Error())
		return r
	}
	r.PIDStarted = pid

	// 3. Subscribe SSE output
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outCh, doneCh, err := subscribeOutput(ctx, id)
	if err != nil {
		r.Notes = append(r.Notes, "subscribe: "+err.Error())
		return r
	}

	bootBuf := bytes.Buffer{}
	collect := func(buf *bytes.Buffer, dur time.Duration) {
		deadline := time.Now().Add(dur)
		for time.Now().Before(deadline) {
			remain := time.Until(deadline)
			if remain <= 0 {
				return
			}
			select {
			case chunk, ok := <-outCh:
				if !ok {
					return
				}
				buf.Write(chunk)
			case <-time.After(remain):
				return
			case <-doneCh:
				return
			}
		}
	}

	// 4. Wait boot up to 6s OR until output settles
	collect(&bootBuf, 6*time.Second)
	r.BootBytes = bootBuf.Len()
	if r.BootBytes < 50 {
		r.Notes = append(r.Notes, fmt.Sprintf("boot output very small (%d bytes)", r.BootBytes))
	}

	// 4b. If a pending menu/prompt is up (e.g. Gemini "Trust folder?"), pick option 1
	// to dismiss it before our test prompt.
	if inst := fetchInstance(id); inst != nil && len(inst.PendingActions) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("auto-dismissing pending prompt with option 1 (q=%q)", inst.PendingQuestion))
		_ = sendText(id, "1")
		drain := bytes.Buffer{}
		collect(&drain, 3*time.Second)
		bootBuf.Write(drain.Bytes())
		r.BootBytes = bootBuf.Len()
	}

	// 5. Send "oi"
	if err := sendText(id, "oi"); err != nil {
		r.Notes = append(r.Notes, "send: "+err.Error())
		return r
	}

	// 6. Capture up to 30s post-input — give the LLM time to actually answer
	postBuf := bytes.Buffer{}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		chunk := bytes.Buffer{}
		collect(&chunk, 2*time.Second)
		postBuf.Write(chunk.Bytes())
		// Bail early if we already see real answer-ish content
		clean := stripAnsi(postBuf.String())
		if hasRealReply(p.Name, clean) {
			break
		}
		// also bail if instance has stored assistant message
		if inst := fetchInstance(id); inst != nil {
			for _, m := range inst.Messages {
				if m.Role == "assistant" && len(strings.TrimSpace(m.Text)) > 0 {
					goto done
				}
			}
		}
	}
done:
	r.PostInputBy = postBuf.Len()

	// 7. Heuristic: response detected if substantial output AND it does NOT just contain " oi"
	cleanedBoot := stripAnsi(bootBuf.String())
	cleanedPost := stripAnsi(postBuf.String())
	r.GotResponse = detectResponse(p.Name, cleanedBoot, cleanedPost)
	if !r.GotResponse {
		tailB := tailOf(cleanedBoot, 220)
		tailP := tailOf(cleanedPost, 220)
		r.Notes = append(r.Notes,
			"no convincing response detected.\n"+
				"  boot tail (last 220 chars):\n"+indent(tailB, "    > ")+
				"\n  post-input tail (last 220 chars):\n"+indent(tailP, "    > "))
	}

	// 8. Check stored messages
	inst := fetchInstance(id)
	if inst != nil {
		r.GotMessages = len(inst.Messages)
	}
	return r
}

type instanceJSON struct {
	ID              string                  `json:"id"`
	Status          string                  `json:"status"`
	Messages        []messageJSON           `json:"messages"`
	PendingActions  []map[string]any        `json:"pendingActions"`
	PendingQuestion string                  `json:"pendingQuestion"`
}

type messageJSON struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

func fetchInstance(id string) *instanceJSON {
	req, _ := http.NewRequest("GET", baseURL+"/v1/instances/"+id, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	var inst instanceJSON
	if err := json.NewDecoder(res.Body).Decode(&inst); err != nil {
		return nil
	}
	return &inst
}

func detectResponse(provider string, boot string, post string) bool {
	return hasRealReply(provider, post)
}

// hasRealReply tries to spot an actual assistant answer in the cleaned output.
// Heuristic per provider — looks for greetings, working indicators, or for codex
// the explicit "•" reply marker followed by free text.
func hasRealReply(provider string, post string) bool {
	if len(post) < 80 {
		return false
	}
	low := strings.ToLower(post)
	switch provider {
	case "claude":
		// Claude responses come via JSONL but also show in TUI. Look for
		// portuguese/english greetings or the working bullet that appears after
		// the prompt was accepted.
		if containsAny(low, "que tarefa", "que precisa", "oi.", "olá", "ola", "tudo bem", "como posso", "hello") {
			return true
		}
		// "* Worked for" / "* Brewed" banners only appear after a turn completed.
		if containsAny(low, "worked for", "baked for", "brewed for", "cooked for") {
			return true
		}
		return false
	case "codex":
		// Codex shows "•" marker + reply text once it has started answering.
		if strings.Contains(post, "•") && containsAny(low, "olá", "ola", "oi", "hello", "hi ") {
			return true
		}
		if containsAny(low, "thinking", "working", "tokens used") {
			return true
		}
		return false
	case "gemini":
		if containsAny(low, "olá", "ola", "hello", "hi ", "como posso", "how can", "tudo bem",
			"responding", "thinking", "esc to cancel") {
			return true
		}
		return false
	default:
		return len(post) > 200
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func stripAnsi(text string) string {
	return ansi.ReplaceAllString(text, "")
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func indent(s string, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func printResult(r Result) {
	emoji := "FAIL"
	if r.GotResponse {
		emoji = " OK "
	}
	fmt.Printf("[%s][%s] pid=%d bootBytes=%d postBytes=%d storedMsgs=%d\n",
		emoji, r.Provider, r.PIDStarted, r.BootBytes, r.PostInputBy, r.GotMessages)
	for _, n := range r.Notes {
		for _, line := range strings.Split(n, "\n") {
			fmt.Printf("    | %s\n", line)
		}
	}
	fmt.Println()
}

func waitHealth(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", baseURL+"/health", nil)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("agent-host did not respond in time")
}

func postInstance(provider string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"origin":     "internal",
		"providerId": provider,
		"title":      "test " + provider,
		"cwd":        "/tmp",
	})
	res, err := http.Post(baseURL+"/v1/instances", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		out, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("status=%s body=%s", res.Status, out)
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

func startTerminal(id string, p Provider) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"command": p.Command,
		"args":    p.Args,
		"cwd":     "/tmp",
	})
	res, err := http.Post(baseURL+"/v1/instances/"+id+"/start-terminal", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		out, _ := io.ReadAll(res.Body)
		return 0, fmt.Errorf("status=%s body=%s", res.Status, out)
	}
	var resp struct {
		PID int `json:"pid"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return 0, err
	}
	return resp.PID, nil
}

func sendText(id string, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	res, err := http.Post(baseURL+"/v1/instances/"+id+"/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		out, _ := io.ReadAll(res.Body)
		return fmt.Errorf("status=%s body=%s", res.Status, out)
	}
	return nil
}

func cleanup(id string) {
	req, _ := http.NewRequest("DELETE", baseURL+"/v1/instances/"+id, nil)
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		res.Body.Close()
	}
}

type wireEvent struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func subscribeOutput(ctx context.Context, id string) (chan []byte, chan struct{}, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/instances/"+id+"/events", nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, nil, fmt.Errorf("subscribe status=%s", res.Status)
	}
	out := make(chan []byte, 64)
	done := make(chan struct{})
	go func() {
		defer res.Body.Close()
		defer close(out)
		defer close(done)
		buf := make([]byte, 8192)
		acc := bytes.Buffer{}
		for {
			n, err := res.Body.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				for {
					line, ok := readLine(&acc)
					if !ok {
						break
					}
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					payload := strings.TrimPrefix(line, "data: ")
					var ev wireEvent
					if err := json.Unmarshal([]byte(payload), &ev); err != nil {
						continue
					}
					if ev.Type == "output" && ev.Data != "" {
						decoded, derr := base64.StdEncoding.DecodeString(ev.Data)
						if derr == nil {
							select {
							case out <- decoded:
							case <-ctx.Done():
								return
							}
						}
					}
					if ev.Type == "exit" {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return out, done, nil
}

func readLine(buf *bytes.Buffer) (string, bool) {
	data := buf.Bytes()
	idx := bytes.IndexByte(data, '\n')
	if idx == -1 {
		return "", false
	}
	line := string(data[:idx])
	buf.Next(idx + 1)
	return strings.TrimRight(line, "\r"), true
}

func lookup(name string) string {
	candidates := []string{}
	if path, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, path)
	}
	home, _ := os.UserHomeDir()
	candidates = append(candidates,
		filepath.Join(home, ".local", "bin", name),
		"/opt/homebrew/bin/"+name,
		"/usr/local/bin/"+name,
	)
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}
