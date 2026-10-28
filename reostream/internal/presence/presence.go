// Package presence keeps a battery camera present to a recorder without
// waking it. Its stream is made only from what reostream already has: the
// last picture, repeated while nothing happens; each motion clip the
// camera uploads, played in at real speed as it arrives; and the live
// picture whenever someone has woken the camera to watch it.
//
// A recorder such as Frigate can then hold the stream open around the
// clock. Repeated identical pictures are no motion to it, and a clip is
// real motion, so its own detection runs on the footage.
package presence

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/mp4read"
)

// Sink receives the stream's frames: the camera's RTSP output.
type Sink interface {
	Frame(baichuan.Frame)
}

var (
	// idleEvery is how often the still is repeated.
	idleEvery = time.Second
	// liveGrace is how long after the last live or clip frame the still
	// takes over again.
	liveGrace = 1500 * time.Millisecond
	// primeFrames is how many copies of the first still are sent at once,
	// so the RTSP output (which waits for a run of frames before it
	// describes a stream with no audio) is ready straight away.
	primeFrames = 32
)

// Player is one battery camera's presence stream.
type Player struct {
	camera string
	sink   Sink

	mu       sync.Mutex
	codec    string
	still    []byte
	clock    uint32 // the stream's own clock, microseconds
	active   time.Time
	primed   bool
	liveLast uint32
	liveSeen bool
	playing  bool

	clips chan string
}

// New builds the player for camera, writing to sink.
func New(camera string, sink Sink) *Player {
	return &Player{camera: camera, sink: sink, clips: make(chan string, 8)}
}

var (
	regMu    sync.Mutex
	registry = map[string]*Player{}
)

// Register makes p the player for its camera.
func Register(p *Player) {
	regMu.Lock()
	registry[p.camera] = p
	regMu.Unlock()
}

// Lookup returns camera's player, or nil.
func Lookup(camera string) *Player {
	regMu.Lock()
	defer regMu.Unlock()
	return registry[camera]
}

// Run repeats the still and plays queued clips until ctx ends.
func (p *Player) Run(ctx context.Context) {
	tick := time.NewTicker(idleEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case path := <-p.clips:
			p.play(ctx, path)
		case <-tick.C:
			p.mu.Lock()
			if p.still != nil && time.Since(p.active) >= liveGrace {
				p.emitLocked(baichuan.FrameIFrame, p.codec, p.still, uint32(idleEvery/time.Microsecond))
			}
			p.mu.Unlock()
		}
	}
}

// Frame passes a live frame through (the camera is awake because someone
// is watching it), on the stream's own clock. It is the sink the camera's
// on-demand main stream writes to.
func (p *Player) Frame(f baichuan.Frame) {
	if f.Kind != baichuan.FrameIFrame && f.Kind != baichuan.FramePFrame {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playing {
		return // a clip has the stream
	}
	dur := uint32(40_000)
	if p.liveSeen {
		if d := f.Micros - p.liveLast; d > 0 && d < 2_000_000 {
			dur = d
		}
	}
	p.liveLast, p.liveSeen = f.Micros, true
	data := f.Video()
	if f.Kind == baichuan.FrameIFrame && p.adoptLocked(f.Codec, data) {
		p.primeLocked()
	}
	p.emitLocked(f.Kind, f.Codec, data, dur)
	p.active = time.Now()
}

// Seed takes the still from a clip already on disk, without playing it:
// the picture to show until the next clip or live view.
func (p *Player) Seed(path string) {
	t, err := mp4read.Open(path)
	if err != nil {
		return
	}
	defer t.Close()
	k := t.LastKey()
	if k < 0 {
		return
	}
	data, err := t.AnnexB(k)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.adoptLocked(t.Codec, data) {
		p.primeLocked()
	}
}

// SeedFrame takes the still from a keyframe reostream kept from the last
// time the camera was awake.
func (p *Player) SeedFrame(codec string, es []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.adoptLocked(codec, es) {
		p.primeLocked()
	}
}

// Play queues a newly uploaded clip to be played into the stream.
func (p *Player) Play(path string) {
	select {
	case p.clips <- path:
	default:
		log.Printf("presence: %s: too many clips waiting, skipping %s", p.camera, path)
	}
}

func (p *Player) play(ctx context.Context, path string) {
	t, err := mp4read.Open(path)
	if err != nil {
		log.Printf("presence: %s: %v", p.camera, err)
		return
	}
	defer t.Close()
	p.mu.Lock()
	if p.codec != "" && t.Codec != p.codec {
		p.mu.Unlock()
		log.Printf("presence: %s: clip is %s but the stream is %s; not played", p.camera, t.Codec, p.codec)
		p.Seed(path)
		return
	}
	p.playing = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.playing = false
		p.mu.Unlock()
	}()

	scale := t.Timescale
	if scale == 0 {
		scale = 90_000
	}
	started := false
	for i, s := range t.Samples {
		if !started && !s.Key {
			continue // a decoder starts at a keyframe
		}
		started = true
		data, err := t.AnnexB(i)
		if err != nil {
			log.Printf("presence: %s: %v", p.camera, err)
			return
		}
		dur := uint32(uint64(s.Dur) * 1_000_000 / uint64(scale))
		kind := baichuan.FramePFrame
		if s.Key {
			kind = baichuan.FrameIFrame
		}
		p.mu.Lock()
		if s.Key && p.adoptLocked(t.Codec, data) {
			p.primeLocked()
		}
		p.emitLocked(kind, t.Codec, data, dur)
		p.active = time.Now()
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(dur) * time.Microsecond):
		}
	}
	log.Printf("presence: %s: played %s", p.camera, path)
}

// adoptLocked takes data as the still. It reports whether this is the
// stream's first picture, which fixes its codec.
func (p *Player) adoptLocked(codec string, data []byte) bool {
	if p.codec != "" && codec != p.codec {
		return false
	}
	first := p.codec == ""
	p.codec = codec
	p.still = append([]byte(nil), data...)
	return first
}

func (p *Player) primeLocked() {
	if p.primed || p.still == nil {
		return
	}
	p.primed = true
	for i := 0; i < primeFrames; i++ {
		p.emitLocked(baichuan.FrameIFrame, p.codec, p.still, 33_000)
	}
}

func (p *Player) emitLocked(kind baichuan.FrameKind, codec string, data []byte, dur uint32) {
	if codec != p.codec {
		return
	}
	p.clock += dur
	p.sink.Frame(baichuan.Frame{Kind: kind, Codec: codec, Data: data, Micros: p.clock})
}
