package rtsp

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Frigate's camera probe reads avg_frame_rate from ffprobe, which ffmpeg
// takes from the SDP's framerate attribute on RTSP. The camera says its
// frame rate in the info frame that comes before the video.
func TestDescriptionCarriesTheCameraFrameRate(t *testing.T) {
	frames := videoFrames(t)
	s := &Stream{}
	s.Frame(baichuan.Frame{Kind: baichuan.FrameInfo, Width: 3840, Height: 2160, FPS: 20})
	for !s.Ready() {
		s.Frame(frames[0])
	}
	b, err := s.description().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a=framerate:20\r\n") {
		t.Fatalf("no framerate in SDP:\n%s", b)
	}
}

func TestNoFrameRateWhenTheCameraSentNone(t *testing.T) {
	frames := videoFrames(t)
	s := &Stream{}
	for !s.Ready() {
		s.Frame(frames[0])
	}
	b, err := s.description().Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "framerate") {
		t.Fatalf("framerate written with none known:\n%s", b)
	}
}

// The rate must survive the server's own DESCRIBE, which rebuilds each
// media for the reply: it once dropped the rate there while the
// description above still carried it, and Frigate kept showing Unknown.
func TestDescribeReplyCarriesTheFrameRate(t *testing.T) {
	frames := captureFrames(t)
	srv, st := serverWithStream(t, "cam_main")
	// The capture's own info frame says 25.
	feedUntilReady(t, st, frames)

	c, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "DESCRIBE rtsp://%s/cam_main RTSP/1.0\r\nCSeq: 1\r\nAccept: application/sdp\r\n\r\n", srv.Addr())
	var reply []byte
	buf := make([]byte, 4096)
	for !strings.Contains(string(reply), "a=fmtp:96") {
		n, err := c.Read(buf)
		reply = append(reply, buf[:n]...)
		if err != nil {
			break
		}
	}
	if !strings.Contains(string(reply), "a=framerate:25") {
		t.Fatalf("DESCRIBE reply has no framerate:\n%s", reply)
	}
}
