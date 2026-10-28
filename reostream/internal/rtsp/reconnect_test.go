package rtsp

import (
	"bytes"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// videoFrames returns only the video frames of the committed H265 capture,
// from its first keyframe.
func videoFrames(t *testing.T) []baichuan.Frame {
	t.Helper()
	var out []baichuan.Frame
	for _, f := range captureFrames(t) {
		if f.Kind != baichuan.FrameIFrame && f.Kind != baichuan.FramePFrame {
			continue
		}
		if len(out) == 0 && f.Kind != baichuan.FrameIFrame {
			continue
		}
		out = append(out, f)
	}
	if len(out) < 3 {
		t.Fatalf("fixture has %d video frames from its first keyframe, need 3", len(out))
	}
	return out
}

// The RTSP session outlives the camera connection. When the camera comes
// back from a reboot its clock has started over, and a reader following the
// RTP timestamps must see them carry on from where they were, plus the real
// length of the outage, not freeze or leap. Reproduced on 2026-10-19 after
// the fleet's Sunday 02:00 scheduled reboot: a freeze of up to 35 minutes or
// a jump of up to 71, depending on where the old clock had got to.
func TestVideoTimestampsContinueAcrossACameraReboot(t *testing.T) {
	frames := videoFrames(t)
	key, p1, p2 := frames[0], frames[1], frames[2]

	wall := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	s := &Stream{now: func() time.Time { return wall }}
	s.Connected()

	// Fifteen minutes into the camera's uptime, one frame every 40ms. With
	// no audio in sight the stream needs audioProbeFrames frames before it
	// describes itself, so the keyframe is repeated until it does.
	key.Micros = 900_000_000
	p1.Micros = 900_040_000
	for !s.Ready() {
		s.Frame(key)
	}
	wall = wall.Add(40 * time.Millisecond)
	s.Frame(p1)
	s.mu.Lock()
	before := s.lastVideo
	s.mu.Unlock()
	if before != 3600 {
		t.Fatalf("PTS before the reboot = %d, want 3600 (one 40ms frame)", before)
	}

	// The camera goes away for 90 seconds and comes back with its clock at
	// 12 seconds of uptime.
	wall = wall.Add(90 * time.Second)
	s.Connected()
	p2.Micros = 12_000_000
	s.Frame(p2)
	s.mu.Lock()
	after := s.lastVideo
	s.mu.Unlock()
	if want := before + 90*90000; after != want {
		t.Fatalf("first PTS after the reboot = %d, want %d (before + 90s)", after, want)
	}
}

// A keyframe's parameter sets go into the SDP, and a camera that comes back
// from a reboot with different ones (a changed resolution or encoder
// setting) would leave every reader decoding against stale sprop values.
// After a reconnect the first keyframe is checked against the description,
// and the description rebuilt only if they differ: rebuilding drops every
// reader, which is the right price for a changed stream and the wrong one
// for a camera that merely rebooted.
func TestDescriptionIsRebuiltWhenParameterSetsChangeAfterReconnect(t *testing.T) {
	frames := captureFrames(t)
	_, s := serverWithStream(t, "reboot")
	s.Connected()
	feedUntilReady(t, s, frames)
	desc1 := s.description()
	ss1 := s.serverStream()

	// Same camera, same parameter sets: nothing to rebuild, readers stay.
	s.Connected()
	for _, f := range frames {
		s.Frame(f)
	}
	if s.description() != desc1 || s.serverStream() != ss1 {
		t.Fatal("description was rebuilt although the parameter sets did not change")
	}

	// Now a keyframe whose SPS differs by one byte.
	var key baichuan.Frame
	for _, f := range frames {
		if f.Kind == baichuan.FrameIFrame {
			key = f
			break
		}
	}
	var es []byte
	var changedSPS []byte
	for _, n := range splitAnnexB(key.Video()) {
		n = append([]byte(nil), n...)
		if h265NALUType(n) == h265SPS {
			n[len(n)-1] ^= 0x01
			changedSPS = n
		}
		es = append(es, 0, 0, 0, 1)
		es = append(es, n...)
	}
	key.Data = es

	s.Connected()
	s.Frame(key)
	desc2 := s.description()
	if desc2 == desc1 {
		t.Fatal("description was not rebuilt after the SPS changed")
	}
	got := desc2.Medias[0].Formats[0].(*format.H265).SPS
	if !bytes.Equal(got, changedSPS) {
		t.Fatal("rebuilt description does not carry the new SPS")
	}
	if !s.Ready() {
		t.Fatal("stream is not ready after the rebuild")
	}
	if s.serverStream() == ss1 {
		t.Fatal("the old server stream, with the old SDP, is still being served")
	}
}
