package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "attach" {
		fmt.Fprintln(os.Stderr, "usage: agentctl attach <session-id>")
		os.Exit(2)
	}

	sessionID := os.Args[2]
	address := os.Getenv("AGENT_CHAT_ADDR")
	if address == "" {
		address = "127.0.0.1:47656"
	}

	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "ATTACH %s\n", sessionID); err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
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
