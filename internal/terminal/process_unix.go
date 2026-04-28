//go:build darwin || linux

package terminal

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type Process struct {
	sessionID string
	cmd       *exec.Cmd
	pty       *os.File
	onOutput  OutputFunc
	onExit    ExitFunc
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
	}
	go process.readLoop()
	go process.wait()
	return process, nil
}

func (p *Process) SendLine(text string) error {
	_, err := p.pty.Write([]byte(text + "\r"))
	return err
}

func (p *Process) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
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
		if n > 0 && p.onOutput != nil {
			p.onOutput(p.sessionID, string(buffer[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	if p.onExit != nil {
		p.onExit(p.sessionID, err)
	}
}
