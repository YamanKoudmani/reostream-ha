package baichuan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

// buildPrefixedFrame builds a media packet whose payload is metadata
// followed by a picture, declaring the picture's length in the size field
// and the metadata's length in the word at offset 12, the way the cameras
// do. stated is written as-is so a test can also declare a wrong one.
func buildPrefixedFrame(magic []byte, hdrLen int, codec string, stated uint32, meta, picture []byte) []byte {
	b := make([]byte, hdrLen)
	copy(b[0:], magic)
	copy(b[4:], []byte(codec))
	binary.LittleEndian.PutUint32(b[8:], uint32(len(picture)))
	binary.LittleEndian.PutUint32(b[12:], stated)
	binary.LittleEndian.PutUint32(b[16:], 42)
	return append(b, append(meta, picture...)...)
}

// The header declares where the picture starts, so metadata that imitates a
// frame header no longer costs a picture.
//
// This is the failure the declared length exists to end. The metadata here
// contains 00 00 00 01 27 64: a start code, then an SPS header with a
// profile_idc the spec defines, which is everything the search checks for.
// A capture from the live fisheye carried exactly this shape. The search
// stops there, so the metadata is published as the start of the picture and
// the same number of bytes is cut off its tail -- ffmpeg then reports a
// non-existing PPS, and the recorder discards the segment that began there.
func TestTheStatedPrefixBeatsASearchThatMetadataFools(t *testing.T) {
	meta := append([]byte{0x11, 0x22, 0x33, 0x44},
		[]byte{0x00, 0x00, 0x00, 0x01, 0x27, 0x64, 0xaa, 0xbb}...)
	picture := append([]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x64},
		bytes.Repeat([]byte{0x5a}, 40)...)

	// An I frame's header is 8 bytes longer than the 24 the declaration is
	// measured from, so the camera declares the metadata length plus 8.
	pkt := buildPrefixedFrame(magicIFrame, 32, "H264", uint32(len(meta)+8), meta, picture)

	// The hazard is real: on these bytes the search picks the metadata.
	if got := searchPrefix(pkt, 32, "H264", FrameIFrame); got != 4 {
		t.Fatalf("searchPrefix = %d, want 4; this test no longer exercises a search the metadata fools", got)
	}

	d := NewDepacketizer()
	d.Write(pkt, true)
	f, ok := d.Next()
	if !ok {
		t.Fatal("no frame emitted")
	}
	if !bytes.Equal(f.Data, picture) {
		t.Errorf("Data = % x\nwant   % x", f.Data, picture)
	}
	if len(f.Data) != len(picture) {
		t.Errorf("Data is %d bytes, want %d: the picture's tail was cut off",
			len(f.Data), len(picture))
	}
}

// A camera that puts something else in that word must not be able to make
// things worse than the search it replaces.
func TestAnUnusableStatedPrefixFallsBackToTheSearch(t *testing.T) {
	picture := append([]byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x64},
		bytes.Repeat([]byte{0x5a}, 40)...)
	meta := bytes.Repeat([]byte{0x77}, 12)

	for _, tc := range []struct {
		name   string
		stated uint32
	}{
		{"absurdly large", 1 << 20},
		{"points past the picture", uint32(len(meta) + 8 + 99)},
		{"points at the middle of the metadata", 8 + 6},
		{"zero, as a camera that does not fill it in", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkt := buildPrefixedFrame(magicIFrame, 32, "H264", tc.stated, meta, picture)
			d := NewDepacketizer()
			d.Write(pkt, true)
			f, ok := d.Next()
			if !ok {
				t.Fatal("no frame emitted")
			}
			if !bytes.Equal(f.Data, picture) {
				t.Errorf("Data = % x\nwant   % x", f.Data, picture)
			}
		})
	}
}

// forEachPacket replays a capture and hands over each media packet as it sat
// in the depacketizer's buffer: one packet, plus whatever filler the camera
// left after it, since a message that starts a packet discards the rest.
func forEachPacket(t *testing.T, name string, fn func(buf []byte, hdr int, codec string, kind FrameKind)) {
	t.Helper()
	key := AESKey(nonceFromCapture(t, name), "")
	r := NewReader(bytes.NewReader(loadFixture(t, name)))

	var buf []byte
	deliver := func() {
		if len(buf) < 8 {
			return
		}
		hdr, kind := 24, FramePFrame
		switch {
		case match(buf, magicIFrame):
			hdr, kind = 32, FrameIFrame
		case match(buf, magicPFrame):
		default:
			return
		}
		if len(buf) < hdr {
			return
		}
		fn(buf, hdr, string(trimNul(buf[4:8])), kind)
	}
	for i := 0; ; i++ {
		m, err := r.Next()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if i == 1 {
			r.SetAESKey(key)
		}
		if len(m.Payload) == 0 {
			continue
		}
		if m.StartsPacket {
			deliver()
			buf = buf[:0]
		}
		buf = append(buf, m.Payload...)
	}
	deliver()
}

// Every complete packet in the committed captures declares a metadata length
// that lands exactly on a NAL start code, so the search is never reached.
func TestTheCapturesDeclareEveryPrefix(t *testing.T) {
	for _, name := range []string{"stream_s2c.bin", "h265_s2c.bin"} {
		t.Run(name, func(t *testing.T) {
			var stated, fellBack int
			forEachPacket(t, name, func(buf []byte, hdr int, codec string, kind FrameKind) {
				if _, v := statedPrefix(buf, hdr, codec, true); v == prefixStated {
					stated++
				} else {
					fellBack++
				}
			})
			t.Logf("declared and verified: %d, fell back to the search: %d", stated, fellBack)
			if stated == 0 {
				t.Fatal("no packet declared a usable prefix")
			}
			// One packet in h265_s2c.bin is cut off by the end of the
			// capture, which is not a camera that failed to declare.
			if fellBack > 1 {
				t.Errorf("%d packets fell back to the search, want at most 1", fellBack)
			}
		})
	}
}
