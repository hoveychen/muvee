package main

import (
	"fmt"
	"os"
)

var version = "dev"

const usage = `muvee — lightweight self-hosted PaaS

Usage:
  muvee <command> [flags]

Commands:
  server       Start the control-plane API server (embeds React UI)
  agent        Start a worker agent (builder or deploy role)
  authservice  Start the Traefik ForwardAuth sidecar

Run 'muvee <command> --help' for command-specific flags.

Version: %s
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(1)
	}

	// Strip the sub-command from Args so each runXxx sees os.Args[1:] as its own.
	cmd := os.Args[1]
	os.Args = append(os.Args[:1], os.Args[2:]...)

	switch cmd {
	case "server":
		runServer()
	case "agent":
		runAgent()
	case "authservice":
		runAuthservice()
	case "version", "--version", "-v":
		fmt.Println("muvee", version)
	case "help", "--help", "-h":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(1)
	}
}
