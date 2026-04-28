package terminal

import (
	"errors"
	"sync"
)

type OutputFunc func(sessionID string, text string)
type ExitFunc func(sessionID string, err error)

type StartOptions struct {
	SessionID string
	Command   string
	Args      []string
	CWD       string
	OnOutput  OutputFunc
	OnExit    ExitFunc
}

type Manager struct {
	mu        sync.RWMutex
	processes map[string]*Process
}

func NewManager() *Manager {
	return &Manager{
		processes: make(map[string]*Process),
	}
}

func (m *Manager) Start(options StartOptions) (*Process, error) {
	if options.SessionID == "" {
		return nil, errors.New("session id is required")
	}
	if options.Command == "" {
		return nil, errors.New("command is required")
	}

	process, err := startProcess(options)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.processes[options.SessionID] = process
	m.mu.Unlock()
	return process, nil
}

func (m *Manager) SendLine(sessionID string, text string) error {
	m.mu.RLock()
	process := m.processes[sessionID]
	m.mu.RUnlock()
	if process == nil {
		return errors.New("terminal process not found")
	}
	return process.SendLine(text)
}

func (m *Manager) Stop(sessionID string) error {
	m.mu.Lock()
	process := m.processes[sessionID]
	delete(m.processes, sessionID)
	m.mu.Unlock()
	if process == nil {
		return nil
	}
	return process.Stop()
}

func (m *Manager) Forget(sessionID string) {
	m.mu.Lock()
	delete(m.processes, sessionID)
	m.mu.Unlock()
}

func (m *Manager) IsRunning(sessionID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.processes[sessionID] != nil
}
