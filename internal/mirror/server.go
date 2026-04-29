package mirror

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"clichat/internal/terminal"
)

type Status struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	Address string `json:"address"`
	Note    string `json:"note"`
}

type Server struct {
	mu        sync.RWMutex
	status    Status
	manager   *terminal.Manager
	listener  net.Listener
	output    func(sessionID string, data []byte)
	connected func(sessionID string)
	external  map[string]*externalSession
}

type externalSession struct {
	mu   sync.Mutex
	conn net.Conn
}

func NewServer(manager *terminal.Manager, output func(sessionID string, data []byte), connected func(sessionID string)) *Server {
	return &Server{
		manager:   manager,
		output:    output,
		connected: connected,
		external:  make(map[string]*externalSession),
		status: Status{
			Enabled: false,
			Mode:    "local-disabled",
			Address: "",
			Note:    "Attach local ainda nao iniciado.",
		},
	}
}

func (s *Server) Start(address string) error {
	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.listener = listener
	s.status = Status{
		Enabled: true,
		Mode:    "local-attach",
		Address: listener.Addr().String(),
		Note:    "Terminais externos podem usar agentctl run/attach.",
	}
	s.mu.Unlock()

	go s.acceptLoop(listener)
	return nil
}

func (s *Server) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Server) Send(sessionID string, data []byte) bool {
	s.mu.RLock()
	external := s.external[sessionID]
	s.mu.RUnlock()
	if external == nil {
		return false
	}

	external.mu.Lock()
	defer external.mu.Unlock()
	_, err := external.conn.Write(data)
	return err == nil
}

func (s *Server) acceptLoop(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		_, _ = fmt.Fprintln(conn, "ERR missing command")
		_ = conn.Close()
		return
	}

	command, sessionID, err := parseCommandLine(line)
	if err != nil {
		_, _ = fmt.Fprintln(conn, "ERR "+err.Error())
		_ = conn.Close()
		return
	}

	switch command {
	case "ATTACH":
		s.handleAttach(conn, reader, sessionID)
	case "RUN":
		s.handleRun(conn, reader, sessionID)
	default:
		_, _ = fmt.Fprintln(conn, "ERR unsupported command")
		_ = conn.Close()
	}
}

func (s *Server) handleAttach(conn net.Conn, reader *bufio.Reader, sessionID string) {
	defer conn.Close()

	output, unsubscribe, err := s.manager.Subscribe(sessionID)
	if err != nil {
		_, _ = fmt.Fprintln(conn, "ERR "+err.Error())
		return
	}
	defer unsubscribe()

	_, _ = fmt.Fprintf(conn, "OK attached %s\r\n", sessionID)

	done := make(chan struct{}, 2)
	go func() {
		for chunk := range output {
			if _, err := conn.Write(chunk); err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := reader.Read(buffer)
			if n > 0 {
				_ = s.manager.Send(sessionID, buffer[:n])
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
}

func (s *Server) handleRun(conn net.Conn, reader *bufio.Reader, sessionID string) {
	session := &externalSession{conn: conn}

	s.mu.Lock()
	if old := s.external[sessionID]; old != nil {
		_ = old.conn.Close()
	}
	s.external[sessionID] = session
	s.mu.Unlock()
	if s.connected != nil {
		s.connected(sessionID)
	}

	defer func() {
		s.mu.Lock()
		if s.external[sessionID] == session {
			delete(s.external, sessionID)
		}
		s.mu.Unlock()
		_ = conn.Close()
	}()

	_, _ = fmt.Fprintf(conn, "OK running %s\r\n", sessionID)

	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 && s.output != nil {
			chunk := append([]byte(nil), buffer[:n]...)
			s.output(sessionID, chunk)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_, _ = fmt.Fprintln(conn, "ERR "+err.Error())
			}
			return
		}
	}
}

func parseCommandLine(line string) (string, string, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 2 {
		return "", "", errors.New("usage: ATTACH|RUN <session-id>")
	}
	command := strings.ToUpper(fields[0])
	if command != "ATTACH" && command != "RUN" {
		return "", "", errors.New("usage: ATTACH|RUN <session-id>")
	}
	return command, fields[1], nil
}
