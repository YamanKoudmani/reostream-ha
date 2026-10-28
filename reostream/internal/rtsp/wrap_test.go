package rtsp

import (
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

// The camera clock returns to zero every 71.58 minutes, and a reader must
// never see the RTP timestamp go with it.
//
// This is the fault that made the recorder lose video on a 71.5 minute
// cycle: Frigate's reader took the 4,295 second backward step for a stall,
// gave up after 20 seconds, and the recording segment being written at the
// time was discarded. See rtpTime for the measurement. The MPEG-TS output
// was never affected, which is why only RTSP readers suffered.
func TestRTPTimestampsSurviveTheCameraClockWrap(t *testing.T) {
	frames := captureFrames(t)
	srv, st := serverWithStream(t, "wrap")
	feedUntilReady(t, st, frames)

	tr := gortsplib.ProtocolTCP
	c := &gortsplib.Client{Protocol: &tr}
	u, err := base.ParseURL("rtsp://" + srv.Addr() + "/wrap")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	c.Scheme, c.Host = u.Scheme, u.Host
	if err := c.Start(); err != nil {
		t.Fatalf("client start: %v", err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if err := c.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Distinct timestamps, not packets: one keyframe fragments into dozens of
	// RTP packets that all carry the same one, so counting packets can finish
	// the test without a second frame ever being sent, let alone the wrap.
	var mu sync.Mutex
	var stamps []uint32
	enough := make(chan struct{})
	var closed bool
	c.OnPacketRTPAny(func(_ *description.Media, f format.Format, p *rtp.Packet) {
		// Video only. Audio runs on its own clock at its own sample rate, so
		// mixing the two makes every alternation between them look like a
		// step backwards -- the same trap ts.Clock's own comment records.
		switch f.(type) {
		case *format.H265, *format.H264:
		default:
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(stamps) > 0 && stamps[len(stamps)-1] == p.Timestamp {
			return
		}
		stamps = append(stamps, p.Timestamp)
		if len(stamps) >= 10 && !closed {
			closed = true
			close(enough)
		}
	})
	if _, err := c.Play(nil); err != nil {
		t.Fatalf("play: %v", err)
	}

	// Walk the camera clock up to its last 33 milliseconds and over the
	// edge, at one ordinary frame interval per step, reusing real frames so
	// the encoder has genuine NALs to packetize.
	const step = 33_000 // microseconds between frames
	go func() {
		for i := 0; i < 40; i++ {
			f := frames[i%len(frames)]
			// uint32 arithmetic wraps here exactly as the camera's does: the
			// fourth step crosses zero.
			f.Micros = uint32(0xffffffff-3*step) + uint32(i)*step
			st.Frame(f)
			time.Sleep(20 * time.Millisecond)
		}
	}()

	select {
	case <-enough:
	case <-time.After(15 * time.Second):
		t.Fatal("not enough RTP packets reached the client")
	}

	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(stamps); i++ {
		// Within one session the 90 kHz clock has 13.25 hours of headroom,
		// so anything going backwards here is the camera's wrap leaking
		// through rather than the RTP clock's own rollover.
		if stamps[i] < stamps[i-1] {
			t.Fatalf("RTP timestamp went backwards at packet %d: %d -> %d (a drop of %d ticks, %.0fs)",
				i, stamps[i-1], stamps[i],
				stamps[i-1]-stamps[i], float64(stamps[i-1]-stamps[i])/rtpClockHz)
		}
	}
	t.Logf("%d packets, timestamps %d..%d, monotonic across the wrap",
		len(stamps), stamps[0], stamps[len(stamps)-1])
}
