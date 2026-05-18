package main

// Phase 0 spike — CLI subcommand that opens a WebSocket to the control plane's
// `/api/projects/:id/_exec_spike` endpoint and runs an interactive `docker
// exec -ti` against the project container. PTY allocation, SIGWINCH-driven
// resize, raw-mode stdin forwarding. Replaced in P5–P9.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var projectsExecSpikeCmd = &cobra.Command{
	Use:    "_exec_spike ID-OR-NAME -- CMD [ARGS...]",
	Short:  "(spike) Run a command inside the project container with a PTY",
	Hidden: true,
	Args:   cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireAuth(); err != nil {
			return err
		}
		projectID, err := resolveProjectRef(cl, args[0])
		if err != nil {
			return err
		}
		execCmd := args[1:]

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
		u.Path = strings.TrimRight(u.Path, "/") + "/api/projects/" + projectID + "/_exec_spike"

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
		if err := ws.WriteJSON(map[string]interface{}{
			"type": "open_exec",
			"cmd":  execCmd,
			"cols": cols,
			"rows": rows,
		}); err != nil {
			return fmt.Errorf("send open_exec: %w", err)
		}

		var wmu sync.Mutex
		writeFrame := func(f map[string]interface{}) error {
			wmu.Lock()
			defer wmu.Unlock()
			return ws.WriteJSON(f)
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

		// SIGWINCH → resize frame (Unix only; no-op on Windows).
		stopWatchResize := watchTerminalResize(func() {
			c, r := terminalSize()
			_ = writeFrame(map[string]interface{}{"type": "resize", "cols": c, "rows": r})
		})
		defer stopWatchResize()

		// stdin → server (stdio frames).
		stdinErr := make(chan error, 1)
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					if werr := writeFrame(map[string]interface{}{
						"type": "stdio", "stream": "stdin",
						"data": buf[:n],
					}); werr != nil {
						stdinErr <- werr
						return
					}
				}
				if err != nil {
					stdinErr <- err
					return
				}
			}
		}()

		// server → stdout.
		exitCode := 0
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				if oldState != nil {
					_ = term.Restore(stdinFd, oldState)
				}
				return nil
			}
			var f map[string]interface{}
			if err := json.Unmarshal(msg, &f); err != nil {
				continue
			}
			switch f["type"] {
			case "stdio":
				data, _ := f["data"].(string)
				raw, _ := base64.StdEncoding.DecodeString(data)
				os.Stdout.Write(raw)
			case "exit":
				if v, ok := f["code"].(float64); ok {
					exitCode = int(v)
				}
				if oldState != nil {
					_ = term.Restore(stdinFd, oldState)
				}
				if exitCode != 0 {
					os.Exit(exitCode)
				}
				return nil
			case "error":
				if oldState != nil {
					_ = term.Restore(stdinFd, oldState)
				}
				m, _ := f["msg"].(string)
				return fmt.Errorf("server error: %s", m)
			}
		}
	},
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
