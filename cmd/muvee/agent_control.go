package main

// Phase 0 spike — agent-side outbound WebSocket dialer that connects to the
// control plane's `/api/agent/control` endpoint and serves `open_exec` frames
// by shelling out to `docker exec -ti` with a host PTY. Will evolve in P5–P9
// to handle cp, signals, multi-session multiplexing tweaks, binary framing.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type spikeSession struct {
	ptmx io.WriteCloser
	resize func(cols, rows uint16)
	stop func()
}

type spikeAgent struct {
	wmu      sync.Mutex
	ws       *websocket.Conn
	smu      sync.Mutex
	sessions map[string]*spikeSession
}

func (a *spikeAgent) write(f map[string]interface{}) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return a.ws.WriteJSON(f)
}

func (a *spikeAgent) set(session string, s *spikeSession) {
	a.smu.Lock()
	defer a.smu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]*spikeSession{}
	}
	a.sessions[session] = s
}

func (a *spikeAgent) get(session string) *spikeSession {
	a.smu.Lock()
	defer a.smu.Unlock()
	return a.sessions[session]
}

func (a *spikeAgent) drop(session string) {
	a.smu.Lock()
	defer a.smu.Unlock()
	delete(a.sessions, session)
}

// runAgentControlChannel keeps an outbound WebSocket open to the control plane
// for as long as the agent is up. On disconnect it retries with backoff.
func runAgentControlChannel(ctx context.Context, controlPlaneURL, agentSecret string, nodeID uuid.UUID) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := dialAgentControl(ctx, controlPlaneURL, agentSecret, nodeID)
		if err != nil && ctx.Err() == nil {
			log.Printf("agentcontrol: dial err: %v (retrying in %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func dialAgentControl(ctx context.Context, controlPlaneURL, agentSecret string, nodeID uuid.UUID) error {
	u, err := url.Parse(controlPlaneURL)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agent/control"
	q := u.Query()
	q.Set("node_id", nodeID.String())
	u.RawQuery = q.Encode()

	header := http.Header{}
	if agentSecret != "" {
		header.Set("X-Agent-Secret", agentSecret)
	}
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	if err != nil {
		return err
	}
	defer ws.Close()
	log.Printf("agentcontrol: connected to %s", u.String())

	a := &spikeAgent{ws: ws}

	// Heartbeat.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				if err := a.write(map[string]interface{}{"type": "ping"}); err != nil {
					return
				}
			}
		}
	}()

	for {
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var f map[string]interface{}
		if err := json.Unmarshal(msg, &f); err != nil {
			continue
		}
		ftype, _ := f["type"].(string)
		session, _ := f["session"].(string)
		switch ftype {
		case "hello", "pong":
			// no-op
		case "open_exec":
			container, _ := f["container"].(string)
			cmdAny, _ := f["cmd"].([]interface{})
			cmd := make([]string, 0, len(cmdAny))
			for _, v := range cmdAny {
				if s, ok := v.(string); ok {
					cmd = append(cmd, s)
				}
			}
			cols := uint16(80)
			rows := uint16(24)
			if v, ok := f["cols"].(float64); ok && v > 0 {
				cols = uint16(v)
			}
			if v, ok := f["rows"].(float64); ok && v > 0 {
				rows = uint16(v)
			}
			if session == "" || container == "" || len(cmd) == 0 {
				_ = a.write(map[string]interface{}{
					"type": "error", "session": session,
					"msg": "open_exec requires session/container/cmd",
				})
				continue
			}
			go runDockerExecPTYSpike(ctx, a, container, cmd, session, cols, rows)
		case "stdio":
			s := a.get(session)
			if s == nil {
				continue
			}
			data, _ := f["data"].(string)
			raw, _ := base64.StdEncoding.DecodeString(data)
			if len(raw) > 0 {
				_, _ = s.ptmx.Write(raw)
			}
		case "resize":
			s := a.get(session)
			if s == nil || s.resize == nil {
				continue
			}
			cols := uint16(80)
			rows := uint16(24)
			if v, ok := f["cols"].(float64); ok && v > 0 {
				cols = uint16(v)
			}
			if v, ok := f["rows"].(float64); ok && v > 0 {
				rows = uint16(v)
			}
			s.resize(cols, rows)
		case "close":
			if s := a.get(session); s != nil && s.stop != nil {
				s.stop()
			}
		default:
			// ignore
		}
	}
}

// runDockerExecPTYSpike shells out to `docker exec -ti` with a host PTY and
// proxies bytes through ctrlFrame messages on the control WS.
func runDockerExecPTYSpike(ctx context.Context, a *spikeAgent, container string, cmd []string, session string, cols, rows uint16) {
	args := append([]string{"exec", "-ti", container}, cmd...)
	c := exec.CommandContext(ctx, "docker", args...)
	ptmx, err := pty.StartWithSize(c, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		_ = a.write(map[string]interface{}{"type": "error", "session": session, "msg": err.Error()})
		return
	}
	resize := func(cols, rows uint16) {
		_ = pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
	}
	stop := func() {
		_ = ptmx.Close()
		_ = c.Process.Kill()
	}
	a.set(session, &spikeSession{ptmx: ptmx, resize: resize, stop: stop})
	defer a.drop(session)
	defer ptmx.Close()

	// PTY → control plane
	buf := make([]byte, 4096)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			_ = a.write(map[string]interface{}{
				"type": "stdio", "session": session, "stream": "stdout",
				"data": buf[:n],
			})
		}
		if err != nil {
			break
		}
	}

	code := 0
	if err := c.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	_ = a.write(map[string]interface{}{"type": "exit", "session": session, "code": code})
}
