package control

import (
	"sync"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/alarms"
	"github.com/VoltTech21/reostream/internal/config"
)

// A battery camera has no live alert connection; the files it uploads are
// its motion. One ON for a burst of files, one OFF after the last.
func TestUploadedClipsAreMotionForACameraWithNoAlertConnection(t *testing.T) {
	hold := clipMotionHold
	clipMotionHold = 80 * time.Millisecond
	t.Cleanup(func() { clipMotionHold = hold })

	var mu sync.Mutex
	var got []alarms.Change
	r := &eventRelay{
		handle:  func(c alarms.Change) { mu.Lock(); got = append(got, c); mu.Unlock() },
		watched: map[string]bool{"driveway": true},
	}
	r.clipArrived("porch")
	time.Sleep(30 * time.Millisecond)
	r.clipArrived("porch") // the clip after the snapshot
	r.clipArrived("driveway")
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || !got[0].On || got[1].On || got[0].Camera != "porch" || got[0].Kind != "motion" {
		t.Fatalf("got %+v, want porch motion ON then OFF only", got)
	}
}

func TestNoClipMotionWhileTheRelayIsOff(t *testing.T) {
	r := &eventRelay{}
	r.clipArrived("porch") // must not panic or start a timer
	if len(r.clips) != 0 {
		t.Fatal("a timer started with the relay off")
	}
}

// Motion another tool reports for a camera (reoevent, on MQTT) goes out
// once per change and wakes the camera; repeats of the same state, such as
// the relay's own retained message coming back on a shared topic, do
// nothing.
func TestReportedMotionIsRelayedOncePerChangeAndWakes(t *testing.T) {
	var mu sync.Mutex
	var got []alarms.Change
	var woke []bool
	r := &eventRelay{
		handle: func(c alarms.Change) { mu.Lock(); got = append(got, c); mu.Unlock() },
		wake:   func(cam string, on bool) { mu.Lock(); woke = append(woke, on); mu.Unlock() },
	}
	for _, p := range []string{"ON", "ON", "OFF", "OFF", "garbage", "on"} {
		r.motionReported("cam-10", []byte(p))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || !got[0].On || got[1].On || !got[2].On || got[0].Camera != "cam-10" || got[0].Kind != "motion" {
		t.Fatalf("relayed %+v, want ON, OFF, ON for cam-10", got)
	}
	if len(woke) != 3 || !woke[0] || woke[1] || !woke[2] {
		t.Fatalf("wake calls %v, want on, off, on", woke)
	}
}

func TestMotionTopicsMapToTheirCameras(t *testing.T) {
	got := motionTopics([]config.Camera{
		{Name: "cam-10", UID: "X", MotionTopic: "reostream/argus/motion"},
		{Name: "gate"},
	})
	if len(got) != 1 || got["reostream/argus/motion"] != "cam-10" {
		t.Fatalf("motionTopics = %v", got)
	}
}

// Two reostreams on one broker must not share a client id, or the broker
// keeps dropping one for the other (seen 2026-10-26: "motion topics: EOF"
// every 10 s while a test instance ran beside the real one).
func TestMotionClientIDsDiffer(t *testing.T) {
	if a, b := motionClientID(), motionClientID(); a == b {
		t.Fatalf("two subscribers got the same client id %q", a)
	}
}

// Motion from a battery camera waking on its own goes out like any other
// camera's, and nowhere while the relay is off.
func TestBatteryWakeMotionIsRelayed(t *testing.T) {
	var mu sync.Mutex
	var got []alarms.Change
	r := &eventRelay{handle: func(c alarms.Change) { mu.Lock(); got = append(got, c); mu.Unlock() }}
	r.batteryMotion("argus", true)
	r.batteryMotion("argus", false)
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Camera != "argus" || got[0].Kind != "motion" || got[0].On == got[1].On {
		t.Fatalf("got %+v, want argus motion ON and OFF", got)
	}
	(&eventRelay{}).batteryMotion("argus", true) // relay off: must not panic
}
