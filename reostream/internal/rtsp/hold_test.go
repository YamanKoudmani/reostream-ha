package rtsp

import (
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// #13: a camera with hold_picture keeps showing its last picture while it
// sends no video, on its own timeline carried forward a second at a time.
func TestHoldRepeatsTheLastKeyframeOnceTheCameraGoesQuiet(t *testing.T) {
	clock := time.Unix(1000, 0)
	s := &Stream{now: func() time.Time { return clock }}

	if _, ok := s.holdFrame(); ok {
		t.Fatal("held a picture before the camera sent one")
	}
	s.noteRealVideo(baichuan.Frame{Kind: baichuan.FrameIFrame, Codec: "H265", Data: []byte("key"), Micros: 5_000_000})
	s.noteRealVideo(baichuan.Frame{Kind: baichuan.FramePFrame, Codec: "H265", Data: []byte("delta"), Micros: 5_040_000})

	clock = clock.Add(holdAfter - time.Millisecond)
	if _, ok := s.holdFrame(); ok {
		t.Fatal("held a picture before the camera had been quiet for holdAfter")
	}

	clock = clock.Add(time.Millisecond)
	for i, want := range []uint32{6_040_000, 7_040_000} {
		f, ok := s.holdFrame()
		if !ok {
			t.Fatalf("tick %d: no held picture", i)
		}
		if f.Kind != baichuan.FrameIFrame || string(f.Data) != "key" || f.Micros != want {
			t.Fatalf("tick %d: %v %q micros %d, want the keyframe at %d", i, f.Kind, f.Data, f.Micros, want)
		}
	}

	// Live video takes over again, and the next quiet spell starts from it.
	s.noteRealVideo(baichuan.Frame{Kind: baichuan.FramePFrame, Codec: "H265", Data: []byte("live"), Micros: 9_000_000})
	if _, ok := s.holdFrame(); ok {
		t.Fatal("held a picture right after live video returned")
	}
	clock = clock.Add(holdAfter)
	if f, _ := s.holdFrame(); f.Micros != 10_000_000 {
		t.Fatalf("micros %d after the camera went quiet again, want 10000000", f.Micros)
	}
}

// The held keyframe is a copy: the camera's buffer is reused for the next
// frame, and a picture that changes under Hold would be garbage.
func TestHoldKeepsItsOwnCopyOfTheKeyframe(t *testing.T) {
	clock := time.Unix(1000, 0)
	s := &Stream{now: func() time.Time { return clock }}
	buf := []byte("key")
	s.noteRealVideo(baichuan.Frame{Kind: baichuan.FrameIFrame, Codec: "H264", Data: buf})
	copy(buf, "xxx")
	clock = clock.Add(holdAfter)
	if f, _ := s.holdFrame(); string(f.Data) != "key" {
		t.Fatalf("held %q, want the keyframe as it arrived", f.Data)
	}
}
