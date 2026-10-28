package control

import (
	"context"
	"sync"
	"time"

	"github.com/VoltTech21/reostream/internal/bcudp"
)

// batteryEvery is how often a battery camera is asked for its level. The
// question is a UDP search, which the camera answers without waking.
const batteryEvery = 2 * time.Minute

type batteryReading struct {
	percent int
	at      time.Time
	asking  bool
}

type batteryCache struct {
	mu sync.Mutex
	m  map[string]*batteryReading
}

// level returns the last level read for camera, or -1, and starts a fresh
// read in the background when the last one is stale. A camera that is
// awake reports no level; the previous one stands.
func (b *batteryCache) level(camera, address string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[string]*batteryReading{}
	}
	r := b.m[camera]
	if r == nil {
		r = &batteryReading{percent: -1}
		b.m[camera] = r
	}
	if !r.asking && time.Since(r.at) > batteryEvery {
		r.asking = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			info, err := bcudp.Search(ctx, address)
			b.mu.Lock()
			defer b.mu.Unlock()
			r.asking = false
			r.at = time.Now()
			if err == nil && info.Battery >= 0 {
				r.percent = info.Battery
			}
		}()
	}
	return r.percent
}
