package api

// Phase 0 spike — agent ↔ control-plane outbound WebSocket + project exec
// routing. Filenames / paths prefixed _spike will be replaced in P5–P9; the
// frame format is JSON over text messages for debuggability and will switch to
// binary multiplexing in production.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

type ctrlFrame struct {
	Type      string   `json:"type"`
	Session   string   `json:"session,omitempty"`
	Container string   `json:"container,omitempty"`
	Cmd       []string `json:"cmd,omitempty"`
	Stream    string   `json:"stream,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Code      int      `json:"code,omitempty"`
	Msg       string   `json:"msg,omitempty"`
	NodeID    string   `json:"node_id,omitempty"`
	Cols      int      `json:"cols,omitempty"`
	Rows      int      `json:"rows,omitempty"`
}

type agentControlConn struct {
	nodeID uuid.UUID
	ws     *websocket.Conn
	wmu    sync.Mutex

	smu      sync.Mutex
	sessions map[string]chan ctrlFrame
}

func (a *agentControlConn) writeFrame(f ctrlFrame) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return a.ws.WriteJSON(f)
}

func (a *agentControlConn) openSession(session string) chan ctrlFrame {
	a.smu.Lock()
	defer a.smu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]chan ctrlFrame{}
	}
	ch := make(chan ctrlFrame, 32)
	a.sessions[session] = ch
	return ch
}

func (a *agentControlConn) closeSession(session string) {
	a.smu.Lock()
	defer a.smu.Unlock()
	if ch, ok := a.sessions[session]; ok {
		close(ch)
		delete(a.sessions, session)
	}
}

func (a *agentControlConn) dispatch(f ctrlFrame) {
	a.smu.Lock()
	ch := a.sessions[f.Session]
	a.smu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- f:
	default:
		log.Printf("agentcontrol: session %s buffer full, dropping frame %s", f.Session, f.Type)
	}
}

var (
	agentCtrlMu  sync.RWMutex
	agentCtrlMap = map[uuid.UUID]*agentControlConn{}
)

func registerAgentCtrl(c *agentControlConn) {
	agentCtrlMu.Lock()
	defer agentCtrlMu.Unlock()
	if prev := agentCtrlMap[c.nodeID]; prev != nil {
		_ = prev.ws.Close()
	}
	agentCtrlMap[c.nodeID] = c
}

func unregisterAgentCtrl(c *agentControlConn) {
	agentCtrlMu.Lock()
	defer agentCtrlMu.Unlock()
	if agentCtrlMap[c.nodeID] == c {
		delete(agentCtrlMap, c.nodeID)
	}
}

func lookupAgentCtrl(nodeID uuid.UUID) *agentControlConn {
	agentCtrlMu.RLock()
	defer agentCtrlMu.RUnlock()
	return agentCtrlMap[nodeID]
}

var agentCtrlUpgrader = websocket.Upgrader{
	ReadBufferSize:  16 * 1024,
	WriteBufferSize: 16 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// handleAgentControl is the server endpoint that agents dial outbound to
// establish a long-lived bidirectional control channel.
//
// Route: GET /api/agent/control?node_id=<uuid>
// Auth: X-Agent-Secret (via agentSecretMiddleware).
func (s *Server) handleAgentControl(w http.ResponseWriter, r *http.Request) {
	nodeIDStr := r.URL.Query().Get("node_id")
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		http.Error(w, "invalid node_id", http.StatusBadRequest)
		return
	}
	ws, err := agentCtrlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &agentControlConn{nodeID: nodeID, ws: ws}
	registerAgentCtrl(conn)
	defer unregisterAgentCtrl(conn)
	defer ws.Close()
	log.Printf("agentcontrol: node %s connected", nodeID)

	_ = conn.writeFrame(ctrlFrame{Type: "hello", NodeID: nodeID.String()})

	for {
		ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		var f ctrlFrame
		if err := ws.ReadJSON(&f); err != nil {
			log.Printf("agentcontrol: node %s read err: %v", nodeID, err)
			return
		}
		switch f.Type {
		case "ping":
			_ = conn.writeFrame(ctrlFrame{Type: "pong"})
		case "stdio", "exit", "error":
			conn.dispatch(f)
			if f.Type == "exit" || f.Type == "error" {
				conn.closeSession(f.Session)
			}
		default:
			// ignore unknown
		}
	}
}

// handleProjectExecSpike is the CLI-facing endpoint for the Phase 0 spike.
// User-authenticated; routes the session through the right agent control conn.
//
// Route: GET /api/projects/{id}/_exec_spike (WebSocket upgrade)
// First frame from CLI MUST be {type:"open_exec", cmd:[...]}. Server fills in
// container based on the project's running deployment.
func (s *Server) handleProjectExecSpike(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parsePathUUID(w, r, "id")
	if !ok {
		return
	}
	user := auth.UserFromCtx(r.Context())
	allowed, _ := s.store.CanAccessProject(r.Context(), user.ID, projectID, user.Role == store.UserRoleAdmin)
	if !allowed {
		jsonErr(w, nil, 404)
		return
	}
	dep, err := s.store.GetRunningDeploymentByProject(r.Context(), projectID)
	if err != nil {
		jsonErr(w, err, 500)
		return
	}
	if dep == nil || dep.NodeID == nil {
		jsonErr(w, fmt.Errorf("no running deployment"), 404)
		return
	}
	agent := lookupAgentCtrl(*dep.NodeID)
	if agent == nil {
		jsonErr(w, fmt.Errorf("agent for node %s not currently connected to control channel", dep.NodeID), 503)
		return
	}

	ws, err := agentCtrlUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	// Read the first frame from CLI.
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	var open ctrlFrame
	if err := ws.ReadJSON(&open); err != nil {
		return
	}
	if open.Type != "open_exec" || len(open.Cmd) == 0 {
		_ = ws.WriteJSON(ctrlFrame{Type: "error", Msg: "first frame must be open_exec with non-empty cmd"})
		return
	}

	session := uuid.NewString()
	ch := agent.openSession(session)
	defer agent.closeSession(session)

	// Tell the agent to start.
	containerName := "muvee-" + dep.DomainPrefix
	if err := agent.writeFrame(ctrlFrame{
		Type:      "open_exec",
		Session:   session,
		Container: containerName,
		Cmd:       open.Cmd,
		Cols:      open.Cols,
		Rows:      open.Rows,
	}); err != nil {
		_ = ws.WriteJSON(ctrlFrame{Type: "error", Msg: err.Error()})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// agent → CLI
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case f, ok := <-ch:
				if !ok {
					return
				}
				if err := ws.WriteJSON(f); err != nil {
					return
				}
				if f.Type == "exit" || f.Type == "error" {
					return
				}
			}
		}
	}()

	// CLI → agent (only for stdin/resize/signal once 0b lands; 0a ignores)
	for {
		ws.SetReadDeadline(time.Now().Add(5 * time.Minute))
		var f ctrlFrame
		if err := ws.ReadJSON(&f); err != nil {
			break
		}
		f.Session = session
		if err := agent.writeFrame(f); err != nil {
			break
		}
	}
	cancel()
	<-done
}
