package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "attach":
		attach(os.Args[2], nil)
	case "run":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		run(os.Args[2], os.Args[3], os.Args[4:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: agentctl attach <session-id>")
	fmt.Fprintln(os.Stderr, "       agentctl run <session-id> <command> [args...]")
}

func attach(sessionID string, ptyFile *os.File) {
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
		if ptyFile != nil {
			_, _ = io.Copy(ptyFile, os.Stdin)
		} else {
			_, _ = io.Copy(conn, os.Stdin)
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(os.Stdout, reader)
		done <- struct{}{}
	}()
	<-done
}

func run(sessionID string, command string, args []string) {
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

	if _, err := fmt.Fprintf(conn, "RUN %s\n", sessionID); err != nil {
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

	cmd := exec.Command(command, args...)
	file, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
	defer file.Close()

	restore, err := makeRaw()
	if err == nil {
		defer restore()
	}

	done := make(chan struct{}, 3)
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, err := file.Read(buffer)
			if n > 0 {
				chunk := buffer[:n]
				_, _ = os.Stdout.Write(chunk)
				_, _ = conn.Write(chunk)
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(file, os.Stdin)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(file, reader)
		done <- struct{}{}
	}()

	<-done
	_ = cmd.Process.Kill()
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
