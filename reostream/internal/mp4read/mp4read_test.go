package mp4read

import (
	"bytes"
	"testing"
)

// Both codecs a Reolink camera writes: every sample comes back as Annex-B,
// keyframes led by the parameter sets.
func TestReadsBothCodecs(t *testing.T) {
	for file, codec := range map[string]string{"testdata/h264.mp4": "H264", "testdata/h265.mp4": "H265"} {
		tr, err := Open(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if tr.Codec != codec || len(tr.Samples) != 30 || tr.Timescale == 0 {
			t.Fatalf("%s: codec %s, %d samples, timescale %d", file, tr.Codec, len(tr.Samples), tr.Timescale)
		}
		if !tr.Samples[0].Key || tr.LastKey() != 15 {
			t.Fatalf("%s: keyframes at 0 and 15 expected, last key %d", file, tr.LastKey())
		}
		key, err := tr.AnnexB(0)
		if err != nil {
			t.Fatal(err)
		}
		params := len(tr.Params)
		if got := bytes.Count(key, []byte{0, 0, 0, 1}); got < params+1 {
			t.Fatalf("%s: keyframe has %d NALs, want parameter sets (%d) and the picture", file, got, params)
		}
		for i := range tr.Samples {
			if _, err := tr.AnnexB(i); err != nil {
				t.Fatalf("%s sample %d: %v", file, i, err)
			}
		}
		tr.Close()
	}
}
