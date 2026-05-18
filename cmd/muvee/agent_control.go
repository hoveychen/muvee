package main

// Phase 0 spike — agent-side outbound WebSocket dialer that connects to the
// control plane's `/api/agent/control` endpoint and serves `open_exec` frames
// by shelling out to `docker exec -ti` with a host PTY. Will evolve in P8 to
// handle cp; in P5 the wire protocol moved to a shared internal/agentcontrol
// package so the server, agent, and CLI share one Frame definition.

import (
	"context"
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
	"github.com/hoveychen/muvee/internal/agentcontrol"
)

type spikeSession struct {
	// exec-only: PTY master writer + resize closure.
	ptmx   io.WriteCloser
	resize func(cols, rows uint16)
	// cp-upload-only: stdin pipe of `docker cp - <container>:<path>`.
	cpStdin io.WriteCloser
	// shared: tear down the subprocess + any pipes.
	stop func()
}

type spikeAgent struct {
	wmu      sync.Mutex
	ws       *websocket.Conn
	smu      sync.Mutex
	sessions map[string]*spikeSession
}

func (a *spikeAgent) write(f agentcontrol.Frame) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return agentcontrol.WriteFrame(a.ws, f)
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
				if err := a.write(agentcontrol.Frame{Type: agentcontrol.TypePing}); err != nil {
					return
				}
			}
		}
	}()

	for {
		ws.SetReadDeadline(time.Now().Add(120 * time.Second))
		f, err := agentcontrol.ReadFrame(ws)
		if err != nil {
			return err
		}
		switch f.Type {
		case agentcontrol.TypeHello, agentcontrol.TypePong:
			// no-op
		case agentcontrol.TypeOpenExec:
			if f.Session == "" || f.Container == "" || len(f.Cmd) == 0 {
				_ = a.write(agentcontrol.Frame{
					Type: agentcontrol.TypeError, Session: f.Session,
					Msg: "open_exec requires session/container/cmd",
				})
				continue
			}
			cols, rows := uint16(80), uint16(24)
			if f.Cols > 0 {
				cols = uint16(f.Cols)
			}
			if f.Rows > 0 {
				rows = uint16(f.Rows)
			}
			go runDockerExecPTYSpike(ctx, a, f.Container, f.Cmd, f.Session, cols, rows)
		case agentcontrol.TypeOpenCp:
			if f.Session == "" || f.Container == "" || f.Path == "" {
				_ = a.write(agentcontrol.Frame{
					Type: agentcontrol.TypeError, Session: f.Session,
					Msg: "open_cp requires session/container/path",
				})
				continue
			}
			go runDockerCp(ctx, a, f.Container, f.Path, f.Direction, f.Session)
		case agentcontrol.TypeStdio:
			s := a.get(f.Session)
			if s == nil || s.ptmx == nil {
				continue
			}
			if len(f.Data) > 0 {
				_, _ = s.ptmx.Write(f.Data)
			}
		case agentcontrol.TypeCpUpTar:
			s := a.get(f.Session)
			if s == nil || s.cpStdin == nil {
				continue
			}
			if len(f.Data) > 0 {
				_, _ = s.cpStdin.Write(f.Data)
			}
		case agentcontrol.TypeCpEnd:
			s := a.get(f.Session)
			if s == nil || s.cpStdin == nil {
				continue
			}
			_ = s.cpStdin.Close()
		case agentcontrol.TypeResize:
			s := a.get(f.Session)
			if s == nil || s.resize == nil {
				continue
			}
			cols, rows := uint16(80), uint16(24)
			if f.Cols > 0 {
				cols = uint16(f.Cols)
			}
			if f.Rows > 0 {
				rows = uint16(f.Rows)
			}
			s.resize(cols, rows)
		case agentcontrol.TypeClose:
			if s := a.get(f.Session); s != nil && s.stop != nil {
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
		_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeError, Session: session, Msg: err.Error()})
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
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			_ = a.write(agentcontrol.Frame{
				Type:    agentcontrol.TypeStdio,
				Session: session,
				Stream:  agentcontrol.StreamStdout,
				Data:    chunk,
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
	_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeExit, Session: session, Code: code})
}

// runDockerCp shells out to `docker cp` in either direction and proxies the
// tar stream over the control WS. Direction "up" means local→container
// (CLI is sending tar via cp_up_tar frames); direction "down" means
// container→local (agent reads tar from docker cp stdout and sends it back
// via cp_down_tar frames).
func runDockerCp(ctx context.Context, a *spikeAgent, container, path, direction, session string) {
	switch direction {
	case agentcontrol.CpDirectionUp:
		c := exec.CommandContext(ctx, "docker", "cp", "-", container+":"+path)
		stdin, err := c.StdinPipe()
		if err != nil {
			_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeError, Session: session, Msg: err.Error()})
			return
		}
		var stderr strings.Builder
		c.Stderr = &stderr
		if err := c.Start(); err != nil {
			_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeError, Session: session, Msg: err.Error()})
			return
		}
		a.set(session, &spikeSession{
			cpStdin: stdin,
			stop: func() {
				_ = stdin.Close()
				if c.Process != nil {
					_ = c.Process.Kill()
				}
			},
		})
		defer a.drop(session)
		code := 0
		if err := c.Wait(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		if code != 0 && stderr.Len() > 0 {
			_ = a.write(agentcontrol.Frame{
				Type: agentcontrol.TypeStdio, Session: session,
				Stream: agentcontrol.StreamStderr, Data: []byte(stderr.String()),
			})
		}
		_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeExit, Session: session, Code: code})

	case agentcontrol.CpDirectionDown:
		c := exec.CommandContext(ctx, "docker", "cp", container+":"+path, "-")
		stdout, err := c.StdoutPipe()
		if err != nil {
			_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeError, Session: session, Msg: err.Error()})
			return
		}
		var stderr strings.Builder
		c.Stderr = &stderr
		if err := c.Start(); err != nil {
			_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeError, Session: session, Msg: err.Error()})
			return
		}
		a.set(session, &spikeSession{
			stop: func() {
				if c.Process != nil {
					_ = c.Process.Kill()
				}
			},
		})
		defer a.drop(session)
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				_ = a.write(agentcontrol.Frame{
					Type: agentcontrol.TypeCpDownTar, Session: session, Data: chunk,
				})
			}
			if err != nil {
				break
			}
		}
		_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeCpEnd, Session: session})
		code := 0
		if err := c.Wait(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			}
		}
		if code != 0 && stderr.Len() > 0 {
			_ = a.write(agentcontrol.Frame{
				Type: agentcontrol.TypeStdio, Session: session,
				Stream: agentcontrol.StreamStderr, Data: []byte(stderr.String()),
			})
		}
		_ = a.write(agentcontrol.Frame{Type: agentcontrol.TypeExit, Session: session, Code: code})

	default:
		_ = a.write(agentcontrol.Frame{
			Type: agentcontrol.TypeError, Session: session,
			Msg: "open_cp direction must be 'up' or 'down'",
		})
	}
}
