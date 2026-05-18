package main

// `muveectl projects exec` / `projects shell` — interactive `docker exec -ti`
// against the project container, routed through the muvee server's
// `/api/projects/{id}/exec` WebSocket which proxies to the deploy agent's
// outbound control channel. PTY allocation happens on the agent; the CLI puts
// stdin into raw mode and forwards SIGWINCH-driven resize frames.

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/agentcontrol"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var projectsExecCmd = &cobra.Command{
	Use:   "exec ID-OR-NAME -- CMD [ARGS...]",
	Short: "Run a command inside the project container with a PTY (like kubectl exec)",
	Long: `Opens an interactive PTY against the project's running container and
runs the given command. Use '--' to separate muveectl flags from the command
arguments. Authentication uses the current profile.

Examples:
  muveectl projects exec my-project -- ls -la /app
  muveectl projects exec my-project -- sh -c 'env | grep API'`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runProjectExec(args[0], args[1:])
	},
}

var projectsShellCmd = &cobra.Command{
	Use:   "shell ID-OR-NAME",
	Short: "Open an interactive shell inside the project container",
	Long: `Convenience for 'projects exec ID -- /bin/sh'. Falls back to /bin/sh
because most muvee images ship Alpine/distroless where /bin/bash is missing.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runProjectExec(args[0], []string{"/bin/sh"})
	},
}

func runProjectExec(projectRef string, execCmd []string) error {
	if err := requireAuth(); err != nil {
		return err
	}
	projectID, err := resolveProjectRef(cl, projectRef)
	if err != nil {
		return err
	}

	u, err := url.Parse(cl.server)
	if err != nil {
		return fmt.Errorf("parse server: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/projects/" + projectID + "/exec"

	header := http.Header{}
	header.Set("Authorization", "Bearer "+cl.token)
	ws, resp, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial: %s (%d)", err, resp.StatusCode)
		}
		return fmt.Errorf("dial: %w", err)
	}
	defer ws.Close()

	cols, rows := terminalSize()
	if err := agentcontrol.WriteFrame(ws, agentcontrol.Frame{
		Type: agentcontrol.TypeOpenExec,
		Cmd:  execCmd,
		Cols: cols,
		Rows: rows,
	}); err != nil {
		return fmt.Errorf("send open_exec: %w", err)
	}

	var wmu sync.Mutex
	writeFrame := func(f agentcontrol.Frame) error {
		wmu.Lock()
		defer wmu.Unlock()
		return agentcontrol.WriteFrame(ws, f)
	}

	// Put stdin into raw mode if it's a TTY so keystrokes flow byte-by-byte
	// (Ctrl-C → 0x03 byte that the remote PTY interprets as SIGINT).
	stdinFd := int(os.Stdin.Fd())
	var oldState *term.State
	if term.IsTerminal(stdinFd) {
		st, err := term.MakeRaw(stdinFd)
		if err == nil {
			oldState = st
			defer term.Restore(stdinFd, oldState)
		}
	}

	stopWatchResize := watchTerminalResize(func() {
		c, r := terminalSize()
		_ = writeFrame(agentcontrol.Frame{Type: agentcontrol.TypeResize, Cols: c, Rows: r})
	})
	defer stopWatchResize()

	// stdin → server (stdio frames).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				if werr := writeFrame(agentcontrol.Frame{
					Type:   agentcontrol.TypeStdio,
					Stream: agentcontrol.StreamStdin,
					Data:   chunk,
				}); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// server → stdout.
	for {
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			if oldState != nil {
				_ = term.Restore(stdinFd, oldState)
			}
			return nil
		}
		switch f.Type {
		case agentcontrol.TypeStdio:
			if f.Stream == agentcontrol.StreamStderr {
				os.Stderr.Write(f.Data)
			} else {
				os.Stdout.Write(f.Data)
			}
		case agentcontrol.TypeExit:
			if oldState != nil {
				_ = term.Restore(stdinFd, oldState)
			}
			if f.Code != 0 {
				os.Exit(f.Code)
			}
			return nil
		case agentcontrol.TypeError:
			if oldState != nil {
				_ = term.Restore(stdinFd, oldState)
			}
			return fmt.Errorf("server error: %s", f.Msg)
		}
	}
}

func terminalSize() (cols, rows int) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		fd = int(os.Stdin.Fd())
	}
	w, h, err := term.GetSize(fd)
	if err != nil || w == 0 || h == 0 {
		return 80, 24
	}
	return w, h
}
