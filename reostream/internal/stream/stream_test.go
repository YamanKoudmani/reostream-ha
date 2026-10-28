package stream

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/fakecam"
	"github.com/VoltTech21/reostream/internal/hub"
)

func TestRunStopsWhenTheContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// An already canceled context must return promptly rather than dialling.
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Name: "x", Address: "192.0.2.1:9000", Stream: "main"}, nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a canceled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run ignored a canceled context")
	}
}

func TestPingIsNotSelectedAgainstTheFrameChannel(t *testing.T) {
	// Frames arrive about every 40ms, so a timer case in the same select as
	// the frame case is effectively never chosen and the camera times the
	// session out. Run instead checks elapsed time after each message; this
	// exercises that check, shouldPing, directly rather than through a real
	// connection and a real clock.
	now := time.Now()
	if shouldPing(now, now.Add(time.Second), 10*time.Second) {
		t.Error("pinged after 1s with a 10s interval")
	}
	if !shouldPing(now, now.Add(11*time.Second), 10*time.Second) {
		t.Error("did not ping after 11s with a 10s interval")
	}
}

func TestDroppedEarlyAudioCountsADPCMNotAAC(t *testing.T) {
	// An ADPCM camera whose audio leads its video, which happens on every
	// reconnect, must still count toward DroppedAudio even before a muxer
	// exists to carry anything: ADPCM is never carried regardless of
	// arrival order, so waiting for a muxer before counting it would
	// under-report until the next video frame lands.
	if !droppedEarlyAudio(baichuan.FrameADPCM) {
		t.Error("ADPCM arriving before video should count toward DroppedAudio")
	}
	if droppedEarlyAudio(baichuan.FrameAAC) {
		t.Error("AAC arriving before video is a genuine loss, but not an unsupported-codec drop")
	}
}

// connectedSink counts the connection hook and records whether it fired
// before any frame did.
type connectedSink struct {
	mu         sync.Mutex
	connected  int
	frames     int
	info       int
	frameFirst bool
}

func (s *connectedSink) Frame(f baichuan.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames++
	if f.Kind == baichuan.FrameInfo {
		s.info++
	}
	if s.connected == 0 {
		s.frameFirst = true
	}
}

func (s *connectedSink) Connected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected++
}

// An RTSP session outlives the camera connection that feeds it, so its
// timestamp clock has to be told when that connection was re-established:
// after a camera reboot the microsecond counter starts over, and a clock
// that is not told either freezes or jumps (see ts.Clock.Resume). The hook
// fires once per connection, before any frame from it, so the sink can
// treat the next frame as a new timeline.
func TestSinkIsToldWhenTheCameraConnects(t *testing.T) {
	b, err := os.ReadFile("../baichuan/testdata/stream_s2c.bin")
	if err != nil {
		t.Fatal(err)
	}
	cam := fakecam.New(t, b)
	sink := &connectedSink{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = Run(ctx, Config{Name: "t", Address: cam.Addr(), Stream: "sub", Sink: sink}, hub.New(64))

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.frames == 0 {
		t.Fatal("no frames reached the sink")
	}
	if sink.connected != 1 {
		t.Fatalf("Connected called %d times for one connection, want 1", sink.connected)
	}
	if sink.frameFirst {
		t.Fatal("a frame reached the sink before Connected")
	}
	if sink.info == 0 {
		t.Fatal("the camera's info packet, which carries the picture size, never reached the sink")
	}
}
