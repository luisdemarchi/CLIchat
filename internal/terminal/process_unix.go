//go:build darwin || linux

package terminal

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const maxScrollback = 100 * 1024

type Process struct {
	sessionID  string
	providerID string
	cmd        *exec.Cmd
	pty        *os.File
	onOutput   OutputFunc
	onExit     ExitFunc
	mu         sync.RWMutex
	nextSubID  int
	subs       map[int]chan []byte
	readyCh    chan struct{}
	readyOnce  sync.Once
	scrollback []byte
}

func startProcess(options StartOptions) (*Process, error) {
	cmd := exec.Command(options.Command, options.Args...)
	if options.CWD != "" {
		cmd.Dir = options.CWD
	}
	cmd.Env = append(os.Environ(), "PATH="+terminalPath(), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Env = append(cmd.Env, options.Env...)

	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 32, Cols: 120})
	if err != nil {
		return nil, err
	}

	process := &Process{
		sessionID:  options.SessionID,
		providerID: options.ProviderID,
		cmd:        cmd,
		pty:        file,
		onOutput:   options.OnOutput,
		onExit:     options.OnExit,
		subs:       make(map[int]chan []byte),
		readyCh:    make(chan struct{}),
		scrollback: make([]byte, 0, 16384),
	}
	go process.readLoop()
	go process.wait()
	return process, nil
}

func terminalPath() string {
	parts := []string{
		"/opt/homebrew/bin",
		"/opt/homebrew/sbin",
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		parts = append([]string{home + "/.local/bin", home + "/go/bin", home + "/.cargo/bin"}, parts...)
	}
	if current := os.Getenv("PATH"); current != "" {
		parts = append(parts, current)
	}
	return strings.Join(parts, ":")
}

func (p *Process) SendLine(text string) error {
	// Wait for the TUI to emit at least one chunk before pushing input,
	// otherwise early keystrokes are eaten by the spawning shell / boot sequence.
	p.waitReady(10 * time.Second)
	// extra grace so heavy TUIs (claude, codex) finish drawing prompt and
	// settle MCP / auth checks before the pasted text lands.
	time.Sleep(1500 * time.Millisecond)

	// Universal submit sequence proven by github.com/johannesjo/parallel-code
	// for the exact same trio (Claude / Codex / Gemini):
	//   1. \x1b[I (Focus-In escape) re-arms input on TUIs that have focus
	//      tracking enabled and may have sent themselves Focus-Out when the
	//      app took the OS keyboard focus.
	//   2. Plain text (NOT bracketed paste) — Claude/Gemini treat
	//      \x1b[200~..\x1b[201~ as queued paste and never submit it.
	//   3. ~50 ms gap so the TUI's input handler finishes processing the chars
	//      before the Enter keystroke arrives.
	//   4. Lone \r as a separate write — distinguishes "Enter pressed after a
	//      paste" from "Enter embedded inside the paste payload".
	if _, err := p.pty.Write([]byte("\x1b[I")); err != nil {
		return err
	}
	if _, err := p.pty.Write([]byte(text)); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	_, err := p.pty.Write([]byte{'\r'})
	return err
}

func (p *Process) waitReady(d time.Duration) {
	if p.readyCh == nil {
		return
	}
	select {
	case <-p.readyCh:
	case <-time.After(d):
	}
}

func (p *Process) Write(data []byte) error {
	_, err := p.pty.Write(data)
	return err
}

func (p *Process) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(p.pty, &pty.Winsize{Rows: rows, Cols: cols})
}

func (p *Process) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func (p *Process) Subscribe() (<-chan []byte, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextSubID++
	id := p.nextSubID
	ch := make(chan []byte, 128)
	p.subs[id] = ch

	// Send current scrollback to the new subscriber immediately so they have history
	if len(p.scrollback) > 0 {
		ch <- append([]byte(nil), p.scrollback...)
	}

	unsubscribe := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if sub, ok := p.subs[id]; ok {
			delete(p.subs, id)
			close(sub)
		}
	}

	return ch, unsubscribe, nil
}

func (p *Process) Stop() error {
	if p.pty != nil {
		_ = p.pty.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *Process) readLoop() {
	buffer := make([]byte, 4096)
	for {
		n, err := p.pty.Read(buffer)
		if n > 0 {
			chunk := append([]byte(nil), buffer[:n]...)
			p.broadcast(chunk)
			p.appendToScrollback(chunk)
			if p.onOutput != nil {
				p.onOutput(p.sessionID, string(chunk))
			}
			p.readyOnce.Do(func() { close(p.readyCh) })
		}
		if err != nil {
			return
		}
	}
}

func (p *Process) appendToScrollback(chunk []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scrollback = append(p.scrollback, chunk...)
	if len(p.scrollback) > maxScrollback {
		// Keep the last maxScrollback bytes
		p.scrollback = p.scrollback[len(p.scrollback)-maxScrollback:]
	}
}

func (p *Process) broadcast(chunk []byte) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, sub := range p.subs {
		select {
		case sub <- append([]byte(nil), chunk...):
		default:
		}
	}
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	p.closeSubscribers()
	if p.onExit != nil {
		p.onExit(p.sessionID, err)
	}
}

func (p *Process) closeSubscribers() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, sub := range p.subs {
		delete(p.subs, id)
		close(sub)
	}
}
