package stream

import (
	"context"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/fakecam"
	"github.com/VoltTech21/reostream/internal/hub"
)

// retagVideo rewrites the channel byte (the low byte of the encryption
// offset, which a camera echoes from the request) on every video message of
// a capture, so one fixture can stand in for either of the two streams a
// zoom connection carries.
func retagVideo(t *testing.T, fixture []byte, channel byte) []byte {
	t.Helper()
	b := append([]byte(nil), fixture...)
	for off := 0; off+20 <= len(b); {
		h, n, err := baichuan.DecodeHeader(b[off:])
		if err != nil {
			break
		}
		if h.MsgID == baichuan.MsgIDVideo {
			b[off+12] = channel
		}
		off += n + int(h.MsgLen)
	}
	return b
}

// requestedVideo lists the channel and stream bytes of every video request a
// client sent, read from the plaintext headers.
func requestedVideo(t *testing.T, sent []byte) [][2]byte {
	t.Helper()
	var out [][2]byte
	for off := 0; off+20 <= len(sent); {
		h, n, err := baichuan.DecodeHeader(sent[off:])
		if err != nil {
			break
		}
		if h.MsgID == baichuan.MsgIDVideo {
			out = append(out, [2]byte{byte(h.EncOffset), byte(h.EncOffset >> 8)})
		}
		off += n + int(h.MsgLen)
	}
	return out
}

func TestZoomPublishesTheZoomLensOverBaichuan(t *testing.T) {
	cam := fakecam.New(t, retagVideo(t, h265Fixture(t), zoomChannel))
	h := hub.New(64)
	ch, cancelSub := h.Subscribe()
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, Config{Name: "zoom-on-baichuan", Address: cam.Addr(), Stream: "zoom"}, h)

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no zoom frame reached the hub")
	}

	// Channel 0's sub stream first, the one that unlocks the zoom, then
	// the zoom itself on channel 1's main.
	sid := func(s string) byte { return baichuan.StreamID(s) }
	// The fake camera starts replaying video the moment the first request
	// lands, so the first frame can arrive before the second request is
	// recorded: wait for both.
	got := requestedVideo(t, cam.Received())
	for deadline := time.Now().Add(2 * time.Second); len(got) < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		got = requestedVideo(t, cam.Received())
	}
	want := [][2]byte{{0, sid(baichuan.StreamSub)}, {zoomChannel, sid(baichuan.StreamMain)}}
	if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("video requests = %v, want %v first", got, want)
	}
}

func TestZoomDropsTheStreamThatUnlocksIt(t *testing.T) {
	cam := fakecam.New(t, retagVideo(t, h265Fixture(t), 0))
	h := hub.New(64)
	ch, cancelSub := h.Subscribe()
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, Config{Name: "zoom-drops-channel-0", Address: cam.Addr(), Stream: "zoom"}, h)

	select {
	case <-ch:
		t.Fatal("channel 0's frames were published as the zoom")
	case <-time.After(1500 * time.Millisecond):
	}
}

// The zoom lens's sub stream (896x512 on a TrackMix, checked 2026-10-26) is
// asked for the same way as its main: channel 0's sub first, then channel
// 1's sub.
func TestZoomSubAsksForTheZoomLensSubStream(t *testing.T) {
	cam := fakecam.New(t, retagVideo(t, h265Fixture(t), zoomChannel))
	h := hub.New(64)
	ch, cancelSub := h.Subscribe()
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, Config{Name: "zoom-sub-on-baichuan", Address: cam.Addr(), Stream: "zoom_sub"}, h)

	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no zoom sub frame reached the hub")
	}

	sid := func(s string) byte { return baichuan.StreamID(s) }
	// The fake camera starts replaying video the moment the first request
	// lands, so the first frame can arrive before the second request is
	// recorded: wait for both.
	got := requestedVideo(t, cam.Received())
	for deadline := time.Now().Add(2 * time.Second); len(got) < 2 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		got = requestedVideo(t, cam.Received())
	}
	want := [][2]byte{{0, sid(baichuan.StreamSub)}, {zoomChannel, sid(baichuan.StreamSub)}}
	if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("video requests = %v, want %v first", got, want)
	}
}

func TestZoomSubDropsTheStreamThatUnlocksIt(t *testing.T) {
	cam := fakecam.New(t, retagVideo(t, h265Fixture(t), 0))
	h := hub.New(64)
	ch, cancelSub := h.Subscribe()
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, Config{Name: "zoom-sub-drops-channel-0", Address: cam.Addr(), Stream: "zoom_sub"}, h)

	select {
	case <-ch:
		t.Fatal("channel 0's frames were published as the zoom sub")
	case <-time.After(1500 * time.Millisecond):
	}
}
