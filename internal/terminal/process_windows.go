//go:build windows

package terminal

import (
	"io"
	"os/exec"
)

type Process struct {
	sessionID string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	onOutput  OutputFunc
	onExit    ExitFunc
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

func (p *Process) PID() int {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
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
