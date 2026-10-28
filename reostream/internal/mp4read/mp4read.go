// Package mp4read reads the video track of the MP4 files Reolink cameras
// write (to their SD card, and over FTP): one plain, non-fragmented moov
// with an avc1 or hvc1 track. Just enough to hand the samples back as
// Annex-B access units, parameter sets in front of each keyframe.
package mp4read

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Sample is one video access unit in the file.
type Sample struct {
	Offset int64
	Size   uint32
	// Dur is how long it shows, in Track.Timescale units.
	Dur uint32
	Key bool
}

// Track is a file's video track.
type Track struct {
	Codec     string // "H264" or "H265"
	Timescale uint32
	Params    [][]byte // VPS/SPS/PPS or SPS/PPS, without start codes
	Samples   []Sample
	nalLen    int
	f         *os.File
}

// Open reads path's video track. Close the track when done.
func Open(path string) (*Track, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	t := &Track{f: f}
	moov, err := findTop(f, st.Size(), "moov")
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := t.parseMoov(moov); err != nil {
		f.Close()
		return nil, err
	}
	return t, nil
}

// Close closes the file.
func (t *Track) Close() error { return t.f.Close() }

// AnnexB reads sample i as Annex-B, with the parameter sets in front of a
// keyframe so a decoder can start there.
func (t *Track) AnnexB(i int) ([]byte, error) {
	s := t.Samples[i]
	buf := make([]byte, s.Size)
	if _, err := t.f.ReadAt(buf, s.Offset); err != nil {
		return nil, err
	}
	var out []byte
	if s.Key {
		for _, p := range t.Params {
			out = append(out, 0, 0, 0, 1)
			out = append(out, p...)
		}
	}
	for len(buf) >= t.nalLen {
		n := 0
		for i := 0; i < t.nalLen; i++ {
			n = n<<8 | int(buf[i])
		}
		buf = buf[t.nalLen:]
		if n > len(buf) {
			return nil, errors.New("mp4read: NAL runs past its sample")
		}
		out = append(out, 0, 0, 0, 1)
		out = append(out, buf[:n]...)
		buf = buf[n:]
	}
	return out, nil
}

// LastKey is the index of the last keyframe, or -1.
func (t *Track) LastKey() int {
	for i := len(t.Samples) - 1; i >= 0; i-- {
		if t.Samples[i].Key {
			return i
		}
	}
	return -1
}

type box struct {
	typ  string
	body []byte
}

func findTop(r io.ReaderAt, size int64, want string) ([]byte, error) {
	var off int64
	hdr := make([]byte, 16)
	for off+8 <= size {
		if _, err := r.ReadAt(hdr[:8], off); err != nil {
			return nil, err
		}
		n := int64(binary.BigEndian.Uint32(hdr))
		typ := string(hdr[4:8])
		h := int64(8)
		if n == 1 {
			if _, err := r.ReadAt(hdr[8:16], off+8); err != nil {
				return nil, err
			}
			n, h = int64(binary.BigEndian.Uint64(hdr[8:16])), 16
		} else if n == 0 {
			n = size - off
		}
		if n < h {
			return nil, fmt.Errorf("mp4read: bad box %q", typ)
		}
		if typ == want {
			b := make([]byte, n-h)
			if _, err := r.ReadAt(b, off+h); err != nil {
				return nil, err
			}
			return b, nil
		}
		off += n
	}
	return nil, fmt.Errorf("mp4read: no %s box", want)
}

func children(b []byte) []box {
	var out []box
	for len(b) >= 8 {
		n := int(binary.BigEndian.Uint32(b))
		h := 8
		if n == 1 && len(b) >= 16 {
			n, h = int(binary.BigEndian.Uint64(b[8:])), 16
		} else if n == 0 {
			n = len(b)
		}
		if n < h || n > len(b) {
			break
		}
		out = append(out, box{string(b[4:8]), b[h:n]})
		b = b[n:]
	}
	return out
}

func child(b []byte, path ...string) []byte {
	for _, p := range path {
		found := false
		for _, c := range children(b) {
			if c.typ == p {
				b, found = c.body, true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return b
}

func (t *Track) parseMoov(moov []byte) error {
	for _, c := range children(moov) {
		if c.typ != "trak" {
			continue
		}
		stbl := child(c.body, "mdia", "minf", "stbl")
		stsd := child(stbl, "stsd")
		if stbl == nil || len(stsd) < 8 {
			continue
		}
		entries := children(stsd[8:])
		if len(entries) == 0 {
			continue
		}
		e := entries[0]
		if e.typ != "avc1" && e.typ != "hvc1" && e.typ != "hev1" {
			continue
		}
		if len(e.body) < 78 {
			return errors.New("mp4read: short sample entry")
		}
		if mdhd := child(c.body, "mdia", "mdhd"); len(mdhd) >= 20 {
			if mdhd[0] == 1 && len(mdhd) >= 24 {
				t.Timescale = binary.BigEndian.Uint32(mdhd[20:])
			} else {
				t.Timescale = binary.BigEndian.Uint32(mdhd[12:])
			}
		}
		cfg := children(e.body[78:])
		var err error
		if e.typ == "avc1" {
			t.Codec = "H264"
			err = t.parseAvcC(find(cfg, "avcC"))
		} else {
			t.Codec = "H265"
			err = t.parseHvcC(find(cfg, "hvcC"))
		}
		if err != nil {
			return err
		}
		return t.parseTables(stbl)
	}
	return errors.New("mp4read: no H.264 or H.265 video track")
}

func find(bs []box, typ string) []byte {
	for _, b := range bs {
		if b.typ == typ {
			return b.body
		}
	}
	return nil
}

func (t *Track) parseAvcC(b []byte) error {
	if len(b) < 7 {
		return errors.New("mp4read: no avcC")
	}
	t.nalLen = int(b[4]&3) + 1
	p := b[5:]
	for _, count := range []int{int(p[0] & 0x1f), -1} {
		if count == -1 {
			if len(p) < 1 {
				break
			}
			count = int(p[0])
		}
		p = p[1:]
		for i := 0; i < count; i++ {
			if len(p) < 2 {
				return errors.New("mp4read: short avcC")
			}
			n := int(binary.BigEndian.Uint16(p))
			if len(p) < 2+n {
				return errors.New("mp4read: short avcC")
			}
			t.Params = append(t.Params, p[2:2+n])
			p = p[2+n:]
		}
	}
	return nil
}

func (t *Track) parseHvcC(b []byte) error {
	if len(b) < 23 {
		return errors.New("mp4read: no hvcC")
	}
	t.nalLen = int(b[21]&3) + 1
	arrays := int(b[22])
	p := b[23:]
	for a := 0; a < arrays; a++ {
		if len(p) < 3 {
			return errors.New("mp4read: short hvcC")
		}
		count := int(binary.BigEndian.Uint16(p[1:]))
		p = p[3:]
		for i := 0; i < count; i++ {
			if len(p) < 2 {
				return errors.New("mp4read: short hvcC")
			}
			n := int(binary.BigEndian.Uint16(p))
			if len(p) < 2+n {
				return errors.New("mp4read: short hvcC")
			}
			t.Params = append(t.Params, p[2:2+n])
			p = p[2+n:]
		}
	}
	return nil
}

func (t *Track) parseTables(stbl []byte) error {
	u32 := binary.BigEndian.Uint32
	stsz := child(stbl, "stsz")
	if len(stsz) < 12 {
		return errors.New("mp4read: no stsz")
	}
	fixed, count := u32(stsz[4:]), int(u32(stsz[8:]))
	t.Samples = make([]Sample, count)
	for i := range t.Samples {
		if fixed != 0 {
			t.Samples[i].Size = fixed
		} else if 12+4*i+4 <= len(stsz) {
			t.Samples[i].Size = u32(stsz[12+4*i:])
		}
	}
	// Chunk offsets, then which samples each chunk holds.
	var chunks []int64
	if co := child(stbl, "stco"); len(co) >= 8 {
		for i := 0; i < int(u32(co[4:])) && 8+4*i+4 <= len(co); i++ {
			chunks = append(chunks, int64(u32(co[8+4*i:])))
		}
	} else if co := child(stbl, "co64"); len(co) >= 8 {
		for i := 0; i < int(u32(co[4:])) && 8+8*i+8 <= len(co); i++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint64(co[8+8*i:])))
		}
	}
	stsc := child(stbl, "stsc")
	if len(stsc) < 8 || len(chunks) == 0 {
		return errors.New("mp4read: no chunk tables")
	}
	type run struct{ first, per int }
	var runs []run
	for i := 0; i < int(u32(stsc[4:])) && 8+12*i+12 <= len(stsc); i++ {
		runs = append(runs, run{int(u32(stsc[8+12*i:])), int(u32(stsc[8+12*i+4:]))})
	}
	si := 0
	for ci := range chunks {
		per := 0
		for _, r := range runs {
			if ci+1 >= r.first {
				per = r.per
			}
		}
		off := chunks[ci]
		for k := 0; k < per && si < count; k++ {
			t.Samples[si].Offset = off
			off += int64(t.Samples[si].Size)
			si++
		}
	}
	if stts := child(stbl, "stts"); len(stts) >= 8 {
		si := 0
		for i := 0; i < int(u32(stts[4:])) && 8+8*i+8 <= len(stts); i++ {
			n, d := int(u32(stts[8+8*i:])), u32(stts[8+8*i+4:])
			for k := 0; k < n && si < count; k++ {
				t.Samples[si].Dur = d
				si++
			}
		}
	}
	if stss := child(stbl, "stss"); len(stss) >= 8 {
		for i := 0; i < int(u32(stss[4:])) && 8+4*i+4 <= len(stss); i++ {
			if k := int(u32(stss[8+4*i:])) - 1; k >= 0 && k < count {
				t.Samples[k].Key = true
			}
		}
	} else {
		for i := range t.Samples {
			t.Samples[i].Key = true
		}
	}
	return nil
}
