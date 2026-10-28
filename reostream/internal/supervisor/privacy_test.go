package supervisor

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/config"
	"github.com/VoltTech21/reostream/internal/hub"
	"github.com/VoltTech21/reostream/internal/stream"
)

// A camera in privacy mode is not a failing stream: it shows as Privacy,
// is checked again on a steady slow timer rather than the error backoff,
// and its checks are not counted as restarts. When the camera streams
// again, Privacy clears.
func TestPrivacyModeIsCheckedSlowlyAndClears(t *testing.T) {
	retry := privacyRetry
	privacyRetry = 60 * time.Millisecond
	t.Cleanup(func() { privacyRetry = retry })

	var calls int32
	run := func(ctx context.Context, cfg stream.Config, h *hub.Hub) error {
		n := atomic.AddInt32(&calls, 1)
		if n <= 3 {
			return fmt.Errorf("stream: e1: %w", stream.ErrPrivacy)
		}
		<-ctx.Done()
		return nil
	}
	s := New([]config.Camera{{Name: "e1", Address: "192.0.2.1", Streams: []string{"main"}}}, run)
	s.setBackoff(time.Millisecond, time.Millisecond, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool { return atomic.LoadInt32(&calls) >= 1 })
	stat := s.Stats()[0]
	if !stat.Privacy {
		t.Fatalf("stat = %+v, want Privacy", stat)
	}
	// With a 1 ms error backoff, three refusals inside ~120 ms means the
	// privacy timer, not the backoff, set the pace.
	time.Sleep(30 * time.Millisecond)
	if n := atomic.LoadInt32(&calls); n > 1 {
		t.Fatalf("%d checks in 30 ms; privacy mode should be checked every %s", n, privacyRetry)
	}
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) >= 4 })
	waitFor(t, func() bool { return !s.Stats()[0].Privacy })
	if r := s.Stats()[0].Restarts; r != 0 {
		t.Errorf("Restarts = %d; privacy checks are not restarts", r)
	}
}
