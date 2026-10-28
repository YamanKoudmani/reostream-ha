package control

import (
	"context"
	"sync"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// One connection per camera for the pan/tilt pad and presets, kept open
// between commands. A held arrow is a burst of commands a fraction of a
// second apart, and logging in for each one added 0.1 to 1 s to every move
// (a tester's E1 Zoom report, 10-27). It closes once the pad goes quiet,
// so it is not one more session the camera holds for nothing.

// ptzHoldIdle is how long the connection outlives the last command.
const ptzHoldIdle = 30 * time.Second

type ptzHold struct {
	mu    sync.Mutex
	conn  *baichuan.Conn
	timer *time.Timer
}

// withPtzConn runs fn on cam's held PTZ connection, dialling one if there is
// none. A command that fails on a held connection may have failed because
// the connection had gone stale, so that one is closed and fn tried once
// more on a fresh connection; a failure on a fresh one is the answer.
func (s *Server) withPtzConn(ctx context.Context, cam Camera, fn func(*baichuan.Conn) error) error {
	s.ptzMu.Lock()
	if s.ptzHeld == nil {
		s.ptzHeld = map[string]*ptzHold{}
	}
	h := s.ptzHeld[cam.Name]
	if h == nil {
		h = &ptzHold{}
		s.ptzHeld[cam.Name] = h
	}
	s.ptzMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.timer != nil {
		h.timer.Stop()
	}
	for try := 0; try < 2; try++ {
		fresh := false
		if h.conn == nil {
			conn, err := s.dial(ctx, cam)
			if err != nil {
				return err
			}
			h.conn, fresh = conn, true
		}
		err := fn(h.conn)
		if err == nil {
			h.timer = time.AfterFunc(ptzHoldIdle, func() {
				h.mu.Lock()
				defer h.mu.Unlock()
				if h.conn != nil {
					h.conn.Close()
					h.conn = nil
				}
			})
			return nil
		}
		h.conn.Close()
		h.conn = nil
		if fresh || ctx.Err() != nil {
			return err
		}
	}
	return nil
}
