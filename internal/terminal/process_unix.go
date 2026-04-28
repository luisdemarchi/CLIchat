//go:build darwin || linux

package terminal

import (
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type Process struct {
	sessionID string
	cmd       *exec.Cmd
	pty       *os.File
	onOutput  OutputFunc
	onExit    ExitFunc
	mu        sync.RWMutex
	nextSubID int
	subs      map[int]chan []byte
}

func startProcess(options StartOptions) (*Process, error) {
	cmd := exec.Command(options.Command, options.Args...)
	if options.CWD != "" {
		cmd.Dir = options.CWD
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")

	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 32, Cols: 120})
	if err != nil {
		return nil, err
	}

	process := &Process{
		sessionID: options.SessionID,
		cmd:       cmd,
		pty:       file,
		onOutput:  options.OnOutput,
		onExit:    options.OnExit,
		subs:      make(map[int]chan []byte),
	}
	go process.readLoop()
	go process.wait()
	return process, nil
}

func (p *Process) SendLine(text string) error {
	_, err := p.pty.Write([]byte(text + "\r"))
	return err
}

func (p *Process) Write(data []byte) error {
	_, err := p.pty.Write(data)
	return err
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
			if p.onOutput != nil {
				p.onOutput(p.sessionID, string(chunk))
			}
		}
		if err != nil {
			return
		}
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
