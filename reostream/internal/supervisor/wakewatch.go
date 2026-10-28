package supervisor

import (
	"context"
	"log"
	"time"

	"github.com/VoltTech21/reostream/internal/bcudp"
)

// Live motion for a battery camera, with nothing to set up: its PIR wakes
// the camera to record, and a search, which the camera's low-power chip
// answers without waking it, says whether it is asleep. So a camera that
// was asleep at one search and awake at the next, with no session of ours
// to explain it, saw motion. The camera also wakes for the Reolink app, and
// that reads the same: there is nothing on the LAN to tell them apart.
var (
	// wakeWatchEvery is how often each battery camera is searched. A wake
	// is seen within about this long of the motion.
	wakeWatchEvery = time.Second
	// wakeWatchTimeout bounds one search.
	wakeWatchTimeout = 900 * time.Millisecond
	// wakeOursGrace is how long after our own session a wake is still put
	// down to it.
	wakeOursGrace = 5 * time.Second
	// wakeMotionHold is how long one wake counts as motion. The camera
	// records for 10 to 20 s after its PIR fires, and once our stream is
	// up it stays awake for us, so no later search can say when the motion
	// ended; the next motion after that is the next wake.
	wakeMotionHold = 30 * time.Second

	wakeSearch = bcudp.Search
	wakeOurs   = bcudp.Ours
)

// SetMotionHook has f told when a battery camera's wake turns motion on
// (on) and when that motion ends, so it can go out like a wired camera's
// own alerts. f must not block.
func (s *Supervisor) SetMotionHook(f func(camera string, on bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.motionHook = f
}

func (s *Supervisor) hookMotion(camera string, on bool) {
	s.mu.Lock()
	f := s.motionHook
	s.mu.Unlock()
	if f != nil {
		f(camera, on)
	}
}

// watchWakes searches a battery camera every wakeWatchEvery and, when it
// wakes on its own, wakes its main stream for wakeMotionHold, so whatever
// records the presence stream records the motion live, and tells the
// motion hook.
func (s *Supervisor) watchWakes(ctx context.Context, e *entry) {
	name := e.cfg.Name
	tick := time.NewTicker(wakeWatchEvery)
	defer tick.Stop()
	known, asleep := false, false
	var until time.Time // when the motion from the last wake ends; zero when none
	defer func() {
		if !until.IsZero() {
			s.hookMotion(name, false)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if !until.IsZero() && time.Now().After(until) {
			until = time.Time{}
			s.hookMotion(name, false)
		}
		sctx, cancel := context.WithTimeout(ctx, wakeWatchTimeout)
		info, err := wakeSearch(sctx, e.cfg.Address)
		cancel()
		if err != nil {
			// Out of reach for a moment: what it was stands.
			continue
		}
		woke := known && asleep && !info.Asleep
		known, asleep = true, info.Asleep
		if !woke || wakeOurs(info.Addr, wakeOursGrace) {
			continue
		}
		log.Printf("reostream: %s: woke on its own (motion, or the Reolink app)", name)
		if until.IsZero() {
			s.hookMotion(name, true)
		}
		until = time.Now().Add(wakeMotionHold)
		if until.UnixNano() > e.motionUntil.Load() {
			e.motionUntil.Store(until.UnixNano())
		}
	}
}
