//go:build windows

package terminal

import (
	"io"
	"os/exec"
	"sync"
)

type Process struct {
	sessionID string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
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

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	process := &Process{
		sessionID: options.SessionID,
		cmd:       cmd,
		stdin:     stdin,
		onOutput:  options.OnOutput,
		onExit:    options.OnExit,
		subs:      make(map[int]chan []byte),
	}
	go process.readLoop(stdout)
	go process.readLoop(stderr)
	go process.wait()
	return process, nil
}

func (p *Process) SendLine(text string) error {
	_, err := p.stdin.Write([]byte(text + "\n"))
	return err
}

func (p *Process) Write(data []byte) error {
	_, err := p.stdin.Write(data)
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
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (p *Process) readLoop(reader io.Reader) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 && p.onOutput != nil {
			chunk := append([]byte(nil), buffer[:n]...)
			p.broadcast(chunk)
			p.onOutput(p.sessionID, string(chunk))
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
	if p.onExit != nil {
		p.onExit(p.sessionID, err)
	}
}
