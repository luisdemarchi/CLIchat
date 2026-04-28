package mirror

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"agent-chat-local/internal/terminal"
)

type Status struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	Address string `json:"address"`
	Note    string `json:"note"`
}

type Server struct {
	mu       sync.RWMutex
	status   Status
	manager  *terminal.Manager
	listener net.Listener
}

func NewServer(manager *terminal.Manager) *Server {
	return &Server{
		manager: manager,
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
		Note:    "Terminais externos podem anexar com agentctl attach <session-id>.",
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
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		_, _ = fmt.Fprintln(conn, "ERR missing attach command")
		return
	}

	sessionID, err := parseAttachLine(line)
	if err != nil {
		_, _ = fmt.Fprintln(conn, "ERR "+err.Error())
		return
	}

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
				if !errors.Is(err, io.EOF) {
					_, _ = fmt.Fprintln(conn, "ERR "+err.Error())
				}
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
}

func parseAttachLine(line string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 2 || strings.ToUpper(fields[0]) != "ATTACH" {
		return "", errors.New("usage: ATTACH <session-id>")
	}
	return fields[1], nil
}
