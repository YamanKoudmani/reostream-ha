package rtsp

import (
	"testing"
	"time"
)

// A restart must not send RTP time backwards: each run's timeline starts
// from the wall clock, so a later run starts later than an earlier one did.
func TestTimelineStartsFromTheWallClock(t *testing.T) {
	a := wallEpoch90(time.Unix(1_700_000_000, 0))
	b := wallEpoch90(time.Unix(1_700_000_000, 500_000_000))
	if b-a != rtpClockHz/2 {
		t.Fatalf("half a second is %d ticks, want %d", b-a, rtpClockHz/2)
	}
	if now := wallEpoch90(time.Now()); now <= b {
		t.Fatal("a later run starts its timeline earlier")
	}
}
