package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "attach" {
		fmt.Fprintln(os.Stderr, "usage: agentctl attach <session-id>")
		os.Exit(2)
	}

	sessionID := os.Args[2]
	fmt.Printf("attach requested for session %s\n", sessionID)
	fmt.Println("PTY attach ainda sera ligado ao servidor local do app.")
}
