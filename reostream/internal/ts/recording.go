package ts

import (
	"io"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// RecordingWriter muxes the frames of one downloaded recording into a
// single MPEG-TS stream. The muxer is made from the first video frame's
// codec, since a recording's codec is only known once it arrives; audio
// before that, which has no picture to sit beside, is dropped.
type RecordingWriter struct {
	w     io.Writer
	m     *Muxer
	audio bool
	err   error
}

// NewRecordingWriter writes to w. audio says whether the recording has an
// AAC track to declare.
func NewRecordingWriter(w io.Writer, audio bool) *RecordingWriter {
	return &RecordingWriter{w: w, audio: audio}
}

// Write adds one frame.
func (r *RecordingWriter) Write(f baichuan.Frame) error {
	if r.err != nil {
		return r.err
	}
	if r.m == nil {
		if f.Kind != baichuan.FrameIFrame && f.Kind != baichuan.FramePFrame {
			return nil
		}
		var m *Muxer
		var err error
		if r.audio {
			m, err = NewMuxerWithAudio(f.Codec, "aac")
		} else {
			m, err = NewMuxer(f.Codec)
		}
		if err != nil {
			r.err = err
			return err
		}
		r.m = m
		if _, err := r.w.Write(m.Header()); err != nil {
			r.err = err
			return err
		}
	}
	if f.Kind == baichuan.FrameAAC && !r.audio {
		return nil
	}
	if f.Kind == baichuan.FrameADPCM {
		return nil
	}
	if b := r.m.Frame(f); len(b) > 0 {
		if _, err := r.w.Write(b); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
