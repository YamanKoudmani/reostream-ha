package stream

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	mch264 "github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/hub"
	"github.com/VoltTech21/reostream/internal/ts"
)

// nativePaths are the streams a camera also serves over its own RTSP
// server. The TrackMix's zoom lens is the one, as Preview_02_main (1920x1080
// H.264). Run fetches it over Baichuan (see zoomChannel in stream.go); this
// path is the fallback for a camera whose zoom never sends video that way.
var nativePaths = map[string]string{
	"zoom":     "Preview_02_main",
	"zoom_sub": "Preview_02_sub",
}

// nativeRTSPPort is where Reolink cameras serve RTSP. reostream turns the
// service on when it adds a camera (baichuan.EnableServices), which keeps
// this fallback available.
var nativeRTSPPort = "554"

// runNative pulls one stream from the camera's own RTSP server and
// publishes it exactly as Run publishes a Baichuan stream: the same muxer,
// hub and sink, so every output treats it like any other stream.
func runNative(ctx context.Context, cfg Config, h *hub.Hub, path string) (err error) {
	defer h.MarkDisconnected()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("stream: %s: panic: %v\n%s", cfg.Name, r, debug.Stack())
			err = fmt.Errorf("stream: %s: panic: %v", cfg.Name, r)
		}
	}()

	host := cfg.Address
	if hh, _, err := net.SplitHostPort(host); err == nil {
		host = hh
	}
	u, err := base.ParseURL("rtsp://" + url.UserPassword(cfg.Username, cfg.Password).String() + "@" +
		net.JoinHostPort(host, nativeRTSPPort) + "/" + path)
	if err != nil {
		return fmt.Errorf("stream: %s: %w", cfg.Name, err)
	}
	tcp := gortsplib.ProtocolTCP
	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host, Protocol: &tcp}
	if err := c.Start(); err != nil {
		return fmt.Errorf("stream: %s: rtsp: %w", cfg.Name, err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return fmt.Errorf("stream: %s: rtsp describe: %w (is RTSP on for this camera?)", cfg.Name, err)
	}
	var h264 *format.H264
	medi := desc.FindFormat(&h264)
	if medi == nil {
		return fmt.Errorf("stream: %s: the camera's RTSP stream has no H.264 video", cfg.Name)
	}
	dec, err := h264.CreateDecoder()
	if err != nil {
		return fmt.Errorf("stream: %s: %w", cfg.Name, err)
	}
	if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		return fmt.Errorf("stream: %s: rtsp setup: %w", cfg.Name, err)
	}

	frames := make(chan baichuan.Frame, 64)
	sps, pps := h264.SPS, h264.PPS
	c.OnPacketRTP(medi, h264, func(pkt *rtp.Packet) {
		pts, ok := c.PacketPTS(medi, pkt)
		if !ok {
			return
		}
		au, err := dec.Decode(pkt)
		if err != nil || len(au) == 0 {
			return
		}
		key := false
		for _, n := range au {
			switch n[0] & 0x1f {
			case 5:
				key = true
			case 7:
				sps = n
			case 8:
				pps = n
			}
		}
		if key && sps != nil && pps != nil {
			au = append([][]byte{sps, pps}, au...)
		}
		f := baichuan.Frame{Kind: baichuan.FramePFrame, Codec: "H264", Data: annexB(au), Micros: uint32(pts * 1_000_000 / 90_000)}
		if key {
			f.Kind = baichuan.FrameIFrame
		}
		select {
		case frames <- f:
		default: // a stalled reader drops frames rather than the session
		}
	})
	if _, err := c.Play(nil); err != nil {
		return fmt.Errorf("stream: %s: rtsp play: %w", cfg.Name, err)
	}

	h.MarkConnected()
	if cs, ok := cfg.Sink.(ConnectionSink); ok {
		cs.Connected()
	}
	mediaTimeout := cfg.MediaTimeout
	if mediaTimeout <= 0 {
		mediaTimeout = DefaultMediaTimeout
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	timer := time.NewTimer(mediaTimeout)
	defer timer.Stop()
	var mux *ts.Muxer
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return fmt.Errorf("stream: %s: rtsp: %w", cfg.Name, err)
		case <-timer.C:
			return fmt.Errorf("stream: %s: no video frame in %s from the camera's RTSP server", cfg.Name, mediaTimeout)
		case f := <-frames:
			timer.Reset(mediaTimeout)
			if mux == nil {
				if f.Kind != baichuan.FrameIFrame {
					continue
				}
				// Video only: no audio is carried from the camera's RTSP,
				// and a declared audio track that never gets any data
				// leaves a browser's player waiting for it, never playing.
				nm, err := ts.NewMuxer(f.Codec)
				if err != nil {
					return fmt.Errorf("stream: %s: %w", cfg.Name, err)
				}
				mux = nm
				h.SetHeader(mux.Header())
			}
			if cfg.Sink != nil {
				cfg.Sink.Frame(f)
			}
			if pkt, key := mux.FrameWithKey(f); pkt != nil {
				h.PublishKey(pkt, key)
				h.RecordFrame(len(pkt))
			}
			if f.Kind == baichuan.FrameIFrame {
				h.SetKeyframe(f.Codec, f.Video())
			}
		}
	}
}

func annexB(nalus [][]byte) []byte {
	n := 0
	for _, u := range nalus {
		n += 4 + len(u)
	}
	out := make([]byte, 0, n)
	for _, u := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, u...)
	}
	return out
}

// NativeInfo asks the camera's own RTSP server what one native stream is,
// without playing it: its codec and picture size from the DESCRIBE answer.
// An error means the camera does not serve it (or RTSP is off).
func NativeInfo(ctx context.Context, address, username, password, stream string) (codec string, width, height int, err error) {
	path, ok := nativePaths[stream]
	if !ok {
		return "", 0, 0, fmt.Errorf("%q is not a native stream", stream)
	}
	host := address
	if hh, _, e := net.SplitHostPort(host); e == nil {
		host = hh
	}
	u, err := base.ParseURL("rtsp://" + url.UserPassword(username, password).String() + "@" +
		net.JoinHostPort(host, nativeRTSPPort) + "/" + path)
	if err != nil {
		return "", 0, 0, err
	}
	tcp := gortsplib.ProtocolTCP
	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host, Protocol: &tcp}
	if dl, ok := ctx.Deadline(); ok {
		c.ReadTimeout, c.WriteTimeout = time.Until(dl), time.Until(dl)
	}
	if err := c.Start(); err != nil {
		return "", 0, 0, err
	}
	defer c.Close()
	desc, _, err := c.Describe(u)
	if err != nil {
		return "", 0, 0, err
	}
	var h264 *format.H264
	if desc.FindFormat(&h264) == nil {
		return "", 0, 0, fmt.Errorf("no H.264 video in the camera's %s", path)
	}
	var sps mch264.SPS
	if len(h264.SPS) > 0 && sps.Unmarshal(h264.SPS) == nil {
		width, height = sps.Width(), sps.Height()
	}
	return "H264", width, height, nil
}
