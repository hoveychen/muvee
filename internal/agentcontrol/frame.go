// Package agentcontrol defines the wire protocol between the muvee control
// plane and `muvee agent` instances over the outbound `/api/agent/control`
// WebSocket. The same Frame type is also exchanged between the control plane
// and `muveectl` for project exec/cp sessions: the control plane is a stateful
// proxy that pairs a user-facing session with the right agent control channel.
//
// Wire format: one text WebSocket message per Frame, encoded as JSON. Use
// WriteFrame / ReadFrame so the JSON shape stays in sync across server, agent,
// and CLI.
//
// Frame routing (informative):
//
//	hello       server  → agent     announce node ID on connect
//	ping / pong server <→ agent     heartbeat
//	open_exec   server  → agent     start `docker exec -ti <container> <cmd>`
//	stdio       both directions     PTY/stdin payload, with Stream = stdin|stdout|stderr
//	resize      CLI/server → agent  PTY window-size change (Cols, Rows)
//	signal      CLI/server → agent  out-of-band signal (reserved; SIGINT today rides on the raw-mode 0x03 byte)
//	exit        agent   → server    process exited (Code)
//	error       any direction       a fatal protocol/runtime error (Msg)
//	close       CLI/server → agent  client closing the session
//	cp_up_tar   CLI     → agent     P8 placeholder: tar bytes from local → container
//	cp_down_tar agent   → CLI       P8 placeholder: tar bytes from container → local
//	cp_end      either direction    P8 placeholder: end-of-tar marker
package agentcontrol

import (
	"github.com/gorilla/websocket"
)

// Frame is the single envelope exchanged on the agent-control WebSocket.
// Fields are omit-empty so each Type only carries the fields it needs.
type Frame struct {
	Type      string   `json:"type"`
	Session   string   `json:"session,omitempty"`
	Container string   `json:"container,omitempty"`
	Cmd       []string `json:"cmd,omitempty"`
	Stream    string   `json:"stream,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Cols      int      `json:"cols,omitempty"`
	Rows      int      `json:"rows,omitempty"`
	Sig       string   `json:"sig,omitempty"`
	Code      int      `json:"code,omitempty"`
	Msg       string   `json:"msg,omitempty"`
	NodeID    string   `json:"node_id,omitempty"`
	Path      string   `json:"path,omitempty"`      // open_cp: in-container source/dest path
	Direction string   `json:"direction,omitempty"` // open_cp: "up" (local→container) or "down" (container→local)
}

// Frame Type constants. New types MUST be added here so all three sides
// (server, agent, CLI) stay aligned on the protocol vocabulary.
const (
	TypeHello     = "hello"
	TypePing      = "ping"
	TypePong      = "pong"
	TypeOpenExec  = "open_exec"
	TypeOpenCp    = "open_cp"
	TypeStdio     = "stdio"
	TypeResize    = "resize"
	TypeSignal    = "signal"
	TypeExit      = "exit"
	TypeError     = "error"
	TypeClose     = "close"
	TypeCpUpTar   = "cp_up_tar"
	TypeCpDownTar = "cp_down_tar"
	TypeCpEnd     = "cp_end"
)

// open_cp Direction values.
const (
	CpDirectionUp   = "up"
	CpDirectionDown = "down"
)

// Stdio Stream values.
const (
	StreamStdin  = "stdin"
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// WriteFrame sends one frame on the WebSocket. Concurrent callers must
// serialize externally — gorilla/websocket WriteJSON is not goroutine-safe.
func WriteFrame(ws *websocket.Conn, f Frame) error {
	return ws.WriteJSON(f)
}

// ReadFrame reads one frame from the WebSocket.
func ReadFrame(ws *websocket.Conn) (Frame, error) {
	var f Frame
	if err := ws.ReadJSON(&f); err != nil {
		return Frame{}, err
	}
	return f, nil
}
