package bcudp

import (
	"net"
	"sync"
	"time"
)

// A battery camera wakes for any session, ours included, and a search
// only says that it is awake, not why. So every session this process
// opens is noted here, from the first packet of the dial to its close, and
// a camera that woke while one was open (or had just closed) woke for us.
var ours struct {
	mu   sync.Mutex
	open map[string]int
	last map[string]time.Time
}

func oursOpen(ip net.IP) {
	k := ip.String()
	ours.mu.Lock()
	defer ours.mu.Unlock()
	if ours.open == nil {
		ours.open = map[string]int{}
		ours.last = map[string]time.Time{}
	}
	ours.open[k]++
	ours.last[k] = time.Now()
}

func oursClosed(ip net.IP) {
	k := ip.String()
	ours.mu.Lock()
	defer ours.mu.Unlock()
	if ours.open[k] > 0 {
		ours.open[k]--
	}
	ours.last[k] = time.Now()
}

// Ours reports whether this process has a session with the camera at ip
// open, or had one within the last d: anything else that wakes the camera
// is someone else (the Reolink app) or the camera itself (motion).
func Ours(ip net.IP, d time.Duration) bool {
	k := ip.String()
	ours.mu.Lock()
	defer ours.mu.Unlock()
	return ours.open[k] > 0 || time.Since(ours.last[k]) < d
}
