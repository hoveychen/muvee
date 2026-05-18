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
	"github.com/hoveychen/muvee/internal/agentcontrol"
	"github.com/hoveychen/muvee/internal/auth"
	"github.com/hoveychen/muvee/internal/store"
)

type agentControlConn struct {
	nodeID uuid.UUID
	ws     *websocket.Conn
	wmu    sync.Mutex

	smu      sync.Mutex
	sessions map[string]chan agentcontrol.Frame
}

func (a *agentControlConn) writeFrame(f agentcontrol.Frame) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return agentcontrol.WriteFrame(a.ws, f)
}

func (a *agentControlConn) openSession(session string) chan agentcontrol.Frame {
	a.smu.Lock()
	defer a.smu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]chan agentcontrol.Frame{}
	}
	ch := make(chan agentcontrol.Frame, 32)
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

func (a *agentControlConn) dispatch(f agentcontrol.Frame) {
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

	_ = conn.writeFrame(agentcontrol.Frame{Type: agentcontrol.TypeHello, NodeID: nodeID.String()})

	for {
		ws.SetReadDeadline(time.Now().Add(90 * time.Second))
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			log.Printf("agentcontrol: node %s read err: %v", nodeID, err)
			return
		}
		switch f.Type {
		case agentcontrol.TypePing:
			_ = conn.writeFrame(agentcontrol.Frame{Type: agentcontrol.TypePong})
		case agentcontrol.TypeStdio, agentcontrol.TypeCpDownTar, agentcontrol.TypeCpEnd,
			agentcontrol.TypeExit, agentcontrol.TypeError:
			conn.dispatch(f)
			if f.Type == agentcontrol.TypeExit || f.Type == agentcontrol.TypeError {
				conn.closeSession(f.Session)
			}
		default:
			// ignore unknown
		}
	}
}

// handleProjectExec is the CLI-facing WebSocket endpoint for running a command
// inside the project's container via the agent control channel.
//
// Route: GET /api/projects/{id}/exec (WebSocket upgrade)
// First frame from CLI MUST be {type:"open_exec", cmd:[...]}.
func (s *Server) handleProjectExec(w http.ResponseWriter, r *http.Request) {
	s.proxyAgentSession(w, r, func(f agentcontrol.Frame) error {
		if f.Type != agentcontrol.TypeOpenExec || len(f.Cmd) == 0 {
			return fmt.Errorf("first frame must be open_exec with non-empty cmd")
		}
		return nil
	})
}

// handleProjectCp is the CLI-facing WebSocket endpoint for copying files
// to/from the project container.
//
// Route: GET /api/projects/{id}/cp (WebSocket upgrade)
// First frame from CLI MUST be {type:"open_cp", path:"...", direction:"up"|"down"}.
func (s *Server) handleProjectCp(w http.ResponseWriter, r *http.Request) {
	s.proxyAgentSession(w, r, func(f agentcontrol.Frame) error {
		if f.Type != agentcontrol.TypeOpenCp {
			return fmt.Errorf("first frame must be open_cp")
		}
		if f.Path == "" {
			return fmt.Errorf("open_cp.path is required")
		}
		if f.Direction != agentcontrol.CpDirectionUp && f.Direction != agentcontrol.CpDirectionDown {
			return fmt.Errorf("open_cp.direction must be %q or %q", agentcontrol.CpDirectionUp, agentcontrol.CpDirectionDown)
		}
		return nil
	})
}

// proxyAgentSession is the shared body for the CLI-facing exec/cp endpoints:
// it authenticates the user, finds the project's running agent, upgrades the
// WS, reads the CLI's open frame (validated by validateOpen), forwards it to
// the agent with the container name + new session id filled in, and then
// proxies frames in both directions until either side closes.
func (s *Server) proxyAgentSession(w http.ResponseWriter, r *http.Request, validateOpen func(agentcontrol.Frame) error) {
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
	open, err := agentcontrol.ReadFrame(ws)
	if err != nil {
		return
	}
	if err := validateOpen(open); err != nil {
		_ = agentcontrol.WriteFrame(ws, agentcontrol.Frame{Type: agentcontrol.TypeError, Msg: err.Error()})
		return
	}

	session := uuid.NewString()
	ch := agent.openSession(session)
	defer agent.closeSession(session)

	// Forward the open frame to the agent with container name + session id
	// filled in. We trust the CLI's other fields (Cmd/Path/Direction/Cols/...).
	open.Session = session
	open.Container = "muvee-" + dep.DomainPrefix
	if err := agent.writeFrame(open); err != nil {
		_ = agentcontrol.WriteFrame(ws, agentcontrol.Frame{Type: agentcontrol.TypeError, Msg: err.Error()})
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
				if err := agentcontrol.WriteFrame(ws, f); err != nil {
					return
				}
				if f.Type == agentcontrol.TypeExit || f.Type == agentcontrol.TypeError {
					return
				}
			}
		}
	}()

	// CLI → agent (stdin / resize / signal / cp_up_tar / cp_end / close).
	for {
		ws.SetReadDeadline(time.Now().Add(5 * time.Minute))
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			break
		}
		f.Session = session
		if err := agent.writeFrame(f); err != nil {
			break
		}
	}
	// CLI disconnected — tell the agent to terminate this session so the
	// container process and PTY don't outlive the client.
	_ = agent.writeFrame(agentcontrol.Frame{Type: agentcontrol.TypeClose, Session: session})
	cancel()
	<-done
}
