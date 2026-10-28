package talk

import (
	"math"
	"testing"
)

func sine(n, rate int, hz float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))
	}
	return out
}

func TestResampleDoublesEightKToSixteenK(t *testing.T) {
	var r resampler
	out := r.convert(nil, sine(8000, 8000, 440), 8000, 16000)
	if len(out) < 15998 || len(out) > 16002 {
		t.Fatalf("one second at 8 kHz became %d samples at 16 kHz", len(out))
	}
}

// Packet boundaries must not show in the output: converting in RTP sized
// pieces has to give what converting the whole thing at once gives.
func TestResampleIsContinuousAcrossCalls(t *testing.T) {
	in := sine(4000, 8000, 300)
	var whole resampler
	want := whole.convert(nil, in, 8000, 16000)

	var pieces resampler
	var got []int16
	for off := 0; off < len(in); off += 160 {
		got = pieces.convert(got, in[off:off+160], 8000, 16000)
	}
	if len(got) != len(want) {
		t.Fatalf("pieces gave %d samples, whole gave %d", len(got), len(want))
	}
	for i := range want {
		if d := int(got[i]) - int(want[i]); d > 1 || d < -1 {
			t.Fatalf("sample %d: pieces %d, whole %d", i, got[i], want[i])
		}
	}
}

func TestResamplePassesMatchingRatesThrough(t *testing.T) {
	var r resampler
	in := sine(100, 16000, 440)
	if out := r.convert(nil, in, 16000, 16000); len(out) != len(in) || out[50] != in[50] {
		t.Fatal("16 kHz into 16 kHz was altered")
	}
}
