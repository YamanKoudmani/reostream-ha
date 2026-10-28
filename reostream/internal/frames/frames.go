// Package frames serves a camera stream's access units over HTTP, one
// length-prefixed record per frame, for a client that owns its own decoder.
//
// It exists for the cam wall. Every general-purpose player on the wall boxes
// sits one to three seconds behind the camera: RTSP jitter buffers, the
// container demux and the player's own render clock all add their share. A
// client that feeds access units straight into the hardware decoder and
// renders each picture as it comes out has none of those stages, and this
// is the simplest possible feed for it: no container, no RTP, no SDP, just
// the frames as the camera sent them, Annex B parameter sets inline.
//
// Record layout, big-endian, 20 bytes then payload:
//
//	0   "RF"      magic
//	2   u8        version, 1
//	3   u8        kind: 1 keyframe, 2 inter frame, 3 AAC
//	4   u8        codec: 1 H264, 2 H265, 3 AAC
//	5   u8        reserved
//	6   u16       width, 0 if not yet known
//	8   u16       height, 0 if not yet known
//	10  u32       camera microseconds (wraps every 71.6 minutes)
//	14  u32       payload length
//	18  u16       server send time, milliseconds since the epoch mod 65536
//
// The send time lets a client with a synchronized clock measure the
// server-to-screen leg on its own, without a round trip and without
// trusting the camera's clock. It wraps every 65.5 seconds, which is far
// longer than any latency worth measuring.
//
// A subscriber's first record is a keyframe, and a subscriber that falls
// behind is resumed at the next keyframe rather than handed a frame whose
// references it never saw. Nothing here blocks the camera goroutine.
package frames

import (
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

const (
	HeaderLen = 20
	Version   = 1

	KindKey   = 1
	KindInter = 2
	KindAAC   = 3

	CodecH264 = 1
	CodecH265 = 2
	CodecAAC  = 3
)

// Record is one decoded frame record.
type Record struct {
	Kind, Codec   uint8
	Width, Height uint16
	Micros        uint32
	Payload       []byte
}

// Marshal encodes the record, header and payload, into one new slice.
func (r Record) Marshal() []byte {
	b := make([]byte, HeaderLen+len(r.Payload))
	b[0], b[1] = 'R', 'F'
	b[2] = Version
	b[3] = r.Kind
	b[4] = r.Codec
	binary.BigEndian.PutUint16(b[6:], r.Width)
	binary.BigEndian.PutUint16(b[8:], r.Height)
	binary.BigEndian.PutUint32(b[10:], r.Micros)
	binary.BigEndian.PutUint32(b[14:], uint32(len(r.Payload)))
	binary.BigEndian.PutUint16(b[18:], uint16(time.Now().UnixMilli()))
	copy(b[HeaderLen:], r.Payload)
	return b
}

var errBadRecord = errors.New("frames: bad record header")

// ReadRecord reads one record from r. It is what a Go client, or a test,
// uses; the wall's client is not written in Go.
func ReadRecord(r io.Reader) (Record, error) {
	var h [HeaderLen]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return Record{}, err
	}
	if h[0] != 'R' || h[1] != 'F' || h[2] != Version {
		return Record{}, errBadRecord
	}
	rec := Record{
		Kind:   h[3],
		Codec:  h[4],
		Width:  binary.BigEndian.Uint16(h[6:]),
		Height: binary.BigEndian.Uint16(h[8:]),
		Micros: binary.BigEndian.Uint32(h[10:]),
	}
	n := binary.BigEndian.Uint32(h[14:])
	rec.Payload = make([]byte, n)
	if _, err := io.ReadFull(r, rec.Payload); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// subscriberBuffer is how many records a slow reader may have outstanding
// before it is resumed at the next keyframe. At 20 fps it is three seconds,
// which is far more than a client that wants this feed will ever hold.
const subscriberBuffer = 64

type subscriber struct {
	ch      chan []byte
	waitKey bool // true until the next keyframe may be sent
}

// Stream is one camera stream's feed. It implements stream.FrameSink and
// stream.ConnectionSink.
type Stream struct {
	mu            sync.Mutex
	subs          map[*subscriber]struct{}
	width, height uint16
}

func newStream() *Stream {
	return &Stream{subs: make(map[*subscriber]struct{})}
}

// Connected implements stream.ConnectionSink. A new camera connection may
// carry new parameter sets, and every client decodes those from the
// keyframe itself, so nothing is needed here beyond forgetting the size
// until the camera restates it.
func (s *Stream) Connected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.width, s.height = 0, 0
}

// Frame implements stream.FrameSink. It runs on the camera goroutine and
// never blocks: a subscriber that cannot take a record loses it, and the
// frames after it until the next keyframe.
func (s *Stream) Frame(f baichuan.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rec Record
	switch f.Kind {
	case baichuan.FrameInfo:
		if f.Width > 0 && f.Height > 0 && f.Width < 1<<16 && f.Height < 1<<16 {
			s.width, s.height = uint16(f.Width), uint16(f.Height)
		}
		return
	case baichuan.FrameIFrame, baichuan.FramePFrame:
		rec.Kind = KindInter
		if f.Kind == baichuan.FrameIFrame {
			rec.Kind = KindKey
		}
		switch strings.ToLower(f.Codec) {
		case "h264":
			rec.Codec = CodecH264
		case "h265":
			rec.Codec = CodecH265
		default:
			return
		}
		rec.Payload = f.Video()
	case baichuan.FrameAAC:
		rec.Kind, rec.Codec = KindAAC, CodecAAC
		rec.Payload = f.Data
	default:
		return
	}
	if len(s.subs) == 0 {
		return
	}
	rec.Width, rec.Height, rec.Micros = s.width, s.height, f.Micros
	b := rec.Marshal()

	for sub := range s.subs {
		if sub.waitKey {
			if rec.Kind != KindKey {
				continue
			}
			sub.waitKey = false
		}
		select {
		case sub.ch <- b:
		default:
			// Behind. Whatever was queued is still valid; what follows
			// is not until a keyframe starts a fresh reference chain.
			sub.waitKey = true
		}
	}
}

func (s *Stream) subscribe() (*subscriber, func()) {
	sub := &subscriber{ch: make(chan []byte, subscriberBuffer), waitKey: true}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	return sub, func() {
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
	}
}

// Server holds the feeds and serves them at "/<path>.frames".
type Server struct {
	mu      sync.Mutex
	streams map[string]*Stream
}

func New() *Server {
	return &Server{streams: make(map[string]*Stream)}
}

// Add registers a path and returns the stream that feeds it.
func (s *Server) Add(path string) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[path]; ok {
		return st
	}
	st := newStream()
	s.streams[path] = st
	return st
}

// Handles reports whether the request path is one this server serves.
func Handles(r *http.Request) bool {
	return strings.HasSuffix(r.URL.Path, ".frames")
}

// ServeHTTP streams records until the client goes away. Each record is
// flushed on its own: the whole point of this feed is that nothing waits
// for the next frame before the current one leaves.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, ".frames"), "/")
	s.mu.Lock()
	st, ok := s.streams[name]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}

	sub, cancel := st.subscribe()
	defer cancel()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-sub.ch:
			if _, err := w.Write(b); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}
