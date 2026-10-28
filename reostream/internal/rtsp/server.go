package rtsp

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
)

// Server serves camera streams over RTSP.
//
// Paths mirror the HTTP endpoints, so a camera reachable at
// http://host:8560/lounge_sub.ts is also at rtsp://host:8554/lounge_sub.
type Server struct {
	srv *gortsplib.Server

	mu       sync.RWMutex
	streams  map[string]*Stream
	sessions map[*gortsplib.ServerSession]*Stream
	// talking holds the back channel of each session that set one up.
	talking map[*gortsplib.ServerSession]*backChannel
	// drops counts packets dropped per path since they were last
	// reported. See OnStreamWriteError.
	drops map[string]*dropCount
}

// New returns a server bound to listen. UDP is offered alongside TCP but is
// not preferred: a lost RTP packet is a corrupt frame with no retransmit,
// which is worse than the larger header interleaving over TCP costs. Clients
// that support both negotiate TCP first.
func New(listen string) *Server {
	s := &Server{
		streams:  make(map[string]*Stream),
		sessions: make(map[*gortsplib.ServerSession]*Stream),
		talking:  make(map[*gortsplib.ServerSession]*backChannel),
	}
	s.srv = &gortsplib.Server{
		Handler:        s,
		RTSPAddress:    listen,
		UDPRTPAddress:  ":8000",
		UDPRTCPAddress: ":8001",
		// The per-session write ring is drop-on-full, not block-on-full:
		// when it overflows, gortsplib silently discards the packet. A single
		// fisheye keyframe is a ~1 MB single-slice IDR, which FU-A fragments
		// into ~708 RTP packets pushed in one tight burst. The default queue
		// of 256 overflows mid-keyframe whenever the TCP writer lags the
		// burst, punching holes in the slice that decode as "error while
		// decoding MB 0" on the client. go2rtc hides this by draining fast
		// enough that the ring never fills; a direct player over the network
		// does not. 2048 absorbs a full keyframe with headroom for the
		// P-frames queued behind it. Must be a power of two.
		WriteQueueSize: 2048,
	}
	return s
}

// OnStreamWriteError is called by gortsplib when a session's write queue
// is full and a packet has been dropped.
//
// Implementing it at all is the point. Without this method gortsplib logs
// the error itself, one bare line per dropped packet, with no camera and
// no session on it; a build doing that logged about 2,400 lines a minute
// on this fleet. A dropped packet is not news on its own, the queue is
// drop-on-full by design, but a reader falling behind is worth knowing,
// so this counts them and says so once a minute per session.
func (s *Server) OnStreamWriteError(ctx *gortsplib.ServerHandlerOnStreamWriteErrorCtx) {
	// Who is reading. Naming them is the difference between "a reader is
	// not keeping up" and knowing which one: on 2026-10-16 the fisheye path
	// dropped about 3,800 packets roughly every 72 minutes, and answering
	// "which reader" meant instrumenting the recorder from the outside,
	// because this line did not say.
	//
	// Every lookup here is guarded. gortsplib calls this with no session at
	// all when the write failed before one was established, which is what
	// the handler already had to survive.
	reader := "unknown reader"
	if ctx.Session != nil {
		if conns := ctx.Session.Conns(); len(conns) > 0 && conns[0] != nil {
			if nc := conns[0].NetConn(); nc != nil {
				if addr := nc.RemoteAddr(); addr != nil {
					reader = addr.String()
				}
			}
		}
	}

	s.mu.Lock()
	// sessions maps a session to its stream; streams maps a path to the
	// same stream. There are at most a couple of dozen paths, so the
	// reverse lookup is cheaper than a third map to keep in step.
	path := "?"
	if st, ok := s.sessions[ctx.Session]; ok && st != nil {
		for name, candidate := range s.streams {
			if candidate == st {
				path = name
				break
			}
		}
	}
	// A session that is not in the map still knows its own path, which is
	// better than reporting "?" about a real reader.
	if path == "?" && ctx.Session != nil {
		if p := ctx.Session.Path(); p != "" {
			path = p
		}
	}
	if s.drops == nil {
		s.drops = make(map[string]*dropCount)
	}
	// Keyed by reader as well as path, so two readers of one path are not
	// averaged into a single number that blames neither.
	key := path + " " + reader
	d, ok := s.drops[key]
	if !ok {
		d = &dropCount{}
		s.drops[key] = d
	}
	d.n++
	now := time.Now()
	var say uint64
	if now.Sub(d.said) >= time.Minute {
		say, d.n, d.said = d.n, 0, now
	}
	s.mu.Unlock()

	if say > 0 {
		log.Printf("rtsp: %s: %d packets dropped, reader %s is not keeping up", path, say, reader)
	}
}

// dropCount is one path's dropped packets since they were last reported.
type dropCount struct {
	n    uint64
	said time.Time
}

// Add registers a path and returns the stream that feeds it. The returned
// Stream implements stream.FrameSink. Call before Start.
func (s *Server) Add(path string) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := &Stream{srv: s.srv, epoch90: wallEpoch90(time.Now())}
	s.streams[path] = st
	return st
}

// Start begins listening.
func (s *Server) Start() error {
	if err := s.srv.Start(); err != nil {
		return fmt.Errorf("rtsp: listen on %s: %w", s.srv.RTSPAddress, err)
	}
	return nil
}

// Addr reports the address actually bound, which matters when the configured
// port was 0.
func (s *Server) Addr() string {
	return s.srv.RTSPAddress
}

// Close stops the server and releases every stream.
func (s *Server) Close() {
	s.srv.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.streams {
		st.close()
	}
}

// lookup finds a stream that is ready to be described. A stream that exists
// but has not yet seen a keyframe is deliberately not found: describing it
// would mean an SDP with no parameter sets, which a client caches and then
// fails to decode with.
func (s *Server) lookup(path string) (*Stream, bool) {
	// gortsplib hands the path with a leading slash, and a client may add a
	// trailing one. Neither is part of the name a stream was registered under.
	path = strings.Trim(path, "/")

	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.streams[path]
	if !ok || !st.Ready() {
		return nil, false
	}
	return st, true
}

func (s *Server) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	st, ok := s.lookup(ctx.Path)
	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, st.serverStream(), nil
}

func (s *Server) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	st, ok := s.lookup(ctx.Path)
	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	s.mu.Lock()
	s.sessions[ctx.Session] = st
	s.mu.Unlock()
	return &base.Response{StatusCode: base.StatusOK}, st.serverStream(), nil
}

// OnPlay is where a reader starts counting. Packetization is gated on the
// count, so a session that has set up but not played still costs nothing.
func (s *Server) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	s.mu.RLock()
	st := s.sessions[ctx.Session]
	s.mu.RUnlock()
	if st != nil {
		st.addReader()
		s.listenBackChannel(ctx.Session, st)
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// listenBackChannel starts carrying a session's back channel audio to the
// camera, if the client set one up. gortsplib only offers the back channel
// to clients that ask for it with the ONVIF Require header, as go2rtc does,
// so a plain player never sees it.
func (s *Server) listenBackChannel(ss *gortsplib.ServerSession, st *Stream) {
	back, talker := st.backChannel()
	if back == nil || talker == nil {
		return
	}
	setup := false
	for _, m := range ss.Medias() {
		if m == back {
			setup = true
		}
	}
	if !setup {
		return
	}
	bc := newBackChannel(strings.Trim(ss.Path(), "/"), talker)
	s.mu.Lock()
	s.talking[ss] = bc
	s.mu.Unlock()
	for _, f := range back.Formats {
		switch f.(type) {
		case *format.G711, *format.LPCM:
		default:
			continue
		}
		f := f
		ss.OnPacketRTP(back, f, func(pkt *rtp.Packet) {
			bc.packet(f, pkt.Payload)
		})
	}
}

// OnSessionClose has to drop the reader whatever ended the session, which
// includes a client that vanished without TEARDOWN. Leaving the count high
// would keep packetizing into a void for the life of the process.
func (s *Server) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	s.mu.Lock()
	st := s.sessions[ctx.Session]
	delete(s.sessions, ctx.Session)
	bc := s.talking[ctx.Session]
	delete(s.talking, ctx.Session)
	s.mu.Unlock()
	if st != nil {
		st.removeReader()
	}
	if bc != nil {
		bc.close()
	}
}

// Path returns the RTSP path for one camera stream.
//
// It mirrors the HTTP endpoints so the two outputs name the same feed the
// same way: main is the bare camera name, and the other two take the suffix
// they have on the HTTP side. A camera at http://host:8560/lounge_sub.ts is
// at rtsp://host:8554/lounge_sub.
func Path(camera, stream string) string {
	switch stream {
	case "main":
		return camera
	default:
		return camera + "_" + stream
	}
}
