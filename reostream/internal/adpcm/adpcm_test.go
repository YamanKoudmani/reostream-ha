package adpcm

import (
	"math"
	"testing"
)

func tone(n int, hz, rate float64, amp int16) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(float64(amp) * math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
	return out
}

func TestBlockIsTheSizeTheCamerasAskFor(t *testing.T) {
	var e Encoder
	got := e.EncodeBlock(tone(SamplesPerBlock, 440, 16000, 8000))
	if len(got) != BlockBytes {
		t.Fatalf("block = %d bytes, want %d", len(got), BlockBytes)
	}
	if BlockBytes != 516 {
		t.Errorf("BlockBytes = %d; the cameras ask for lengthPerEncoder 1024, which is 4+512", BlockBytes)
	}
}

// A lossy codec cannot be checked for equality, so this checks it is lossy in
// the way ADPCM is meant to be: the decoded signal tracks the original
// closely rather than diverging.
func TestRoundTripTracksTheSignal(t *testing.T) {
	src := tone(SamplesPerBlock, 440, 16000, 12000)
	var e Encoder
	got := Decode(e.EncodeBlock(src))

	if len(got) != len(src) {
		t.Fatalf("decoded %d samples, want %d", len(got), len(src))
	}

	var sig, noise float64
	for i := range src {
		d := float64(src[i]) - float64(got[i])
		sig += float64(src[i]) * float64(src[i])
		noise += d * d
	}
	snr := 10 * math.Log10(sig/noise)
	// IMA ADPCM at 4 bits per sample is good for roughly 20 dB and up on a
	// tone. Anything below 15 means the predictor and the reconstruction have
	// drifted apart, which is the failure this catches.
	if snr < 15 {
		t.Errorf("round trip SNR = %.1f dB, want at least 15", snr)
	}
}

// The preamble must describe the state the decoder starts from, so decoding a
// block in isolation has to produce the same samples as decoding it in
// sequence. Writing the post-block state instead is an easy mistake and
// sounds like a click at every block boundary.
func TestPreambleDescribesTheStartOfItsOwnBlock(t *testing.T) {
	src := tone(SamplesPerBlock*3, 300, 16000, 10000)

	var e Encoder
	var blocks [][]byte
	for i := 0; i < 3; i++ {
		blocks = append(blocks, e.EncodeBlock(src[i*SamplesPerBlock:(i+1)*SamplesPerBlock]))
	}

	// Decode the third block cold. If its preamble is right, this matches what
	// a decoder walking all three blocks would produce for those samples.
	cold := Decode(blocks[2])
	if len(cold) != SamplesPerBlock {
		t.Fatalf("decoded %d samples", len(cold))
	}

	var sig, noise float64
	third := src[2*SamplesPerBlock:]
	for i := range third {
		d := float64(third[i]) - float64(cold[i])
		sig += float64(third[i]) * float64(third[i])
		noise += d * d
	}
	if snr := 10 * math.Log10(sig/noise); snr < 15 {
		t.Errorf("third block decoded cold: SNR = %.1f dB, want at least 15", snr)
	}
}

func TestShortBlockIsPaddedNotTruncated(t *testing.T) {
	var e Encoder
	got := e.EncodeBlock(tone(100, 440, 16000, 8000))
	if len(got) != BlockBytes {
		t.Errorf("short input gave %d bytes, want a full %d byte block", len(got), BlockBytes)
	}
}

// A custom siren sound is one continuous stream of codes, high nibble first,
// no block preambles: the Reolink app's own upload of 3 s of a quiet room
// decoded that way centers on zero, and the other way drifts (2026-10-26).
func TestEncodeStreamIsHighNibbleFirstAndRoundTrips(t *testing.T) {
	pcm := make([]int16, 1600)
	for i := range pcm {
		// A 500 Hz tone at 16 kHz.
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*500*float64(i)/16000))
	}
	b := EncodeStream(pcm)
	if len(b) != len(pcm)/2 {
		t.Fatalf("len = %d, want %d", len(b), len(pcm)/2)
	}
	got := DecodeStream(b)
	var err float64
	for i := 200; i < len(pcm); i++ {
		err += math.Abs(float64(got[i]) - float64(pcm[i]))
	}
	if avg := err / float64(len(pcm)-200); avg > 400 {
		t.Errorf("mean error %v after round trip", avg)
	}
	// High nibble first: the first sample's code is in b[0]>>4.
	var e Encoder
	if first := e.encodeSample(pcm[0]); b[0]>>4 != first {
		t.Errorf("first code %x is not in the high nibble of %02x", first, b[0])
	}
}

// A talk block is packed first sample in the high nibble, like the Reolink
// app's own talk packets. The round trip above cannot catch the order being
// swapped, since encoder and decoder would swap together, and the camera
// still plays the swapped order as recognizable speech, only rough.
func TestTalkBlockIsHighNibbleFirst(t *testing.T) {
	var e Encoder
	// A loud first sample and silence after: the first code is a big step
	// up (0x7), the second a step back down (sign bit set).
	pcm := make([]int16, SamplesPerBlock)
	pcm[0] = 20000
	b := e.EncodeBlock(pcm)
	if hi, lo := b[4]>>4, b[4]&15; hi != 7 || lo&8 == 0 {
		t.Errorf("first data byte %02x: want the first sample's code 7 in the high nibble and a negative step in the low", b[4])
	}
}
