package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/config"
)

// An error that is kept forever has to say when it happened.
//
// LastError is never cleared, which is deliberate: a stream that failed and
// recovered is worth knowing about. The cost is that the sentence reads in
// the present tense at any age. On 2026-10-16 a fisheye substream reported
// "camera is answering but not sending media (held session)" from a single
// restart hours earlier while it was streaming at 9.8 fps with a frame 0.1s
// old, and that stale sentence was read off the status page and reported as
// a dead feed. The age is what tells the two apart.
func TestALastErrorCarriesWhenItHappened(t *testing.T) {
	s := New([]config.Camera{
		{Name: "fisheye", Address: "192.0.2.1", Streams: []string{"sub"}},
	}, nil)

	if got := s.Stats()[0]; got.LastError != "" || !got.LastErrorAt.IsZero() {
		t.Fatalf("a stream that has not failed reports error %q at %v",
			got.LastError, got.LastErrorAt)
	}

	before := time.Now()
	s.setResult(s.entries["fisheye/sub"], errors.New("held session"))
	after := time.Now()

	got := s.Stats()[0]
	if got.LastError != "held session" {
		t.Errorf("LastError = %q, want %q", got.LastError, "held session")
	}
	if got.LastErrorAt.Before(before) || got.LastErrorAt.After(after) {
		t.Errorf("LastErrorAt = %v, want a time between %v and %v",
			got.LastErrorAt, before, after)
	}

	// Recovering does not erase the error, and must not reset its age
	// either: an hour-old failure stays an hour old once the stream is
	// running again, which is the whole point.
	was := got.LastErrorAt
	s.setRunning(s.entries["fisheye/sub"], true)
	if again := s.Stats()[0]; !again.LastErrorAt.Equal(was) {
		t.Errorf("LastErrorAt moved on recovery: %v, was %v", again.LastErrorAt, was)
	}
}
