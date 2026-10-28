package stream

import (
	"bytes"
	"testing"
)

// The 90 degree SEI, byte for byte, as checked against ffmpeg 6.1 and the
// 7.0 Frigate ships: both decoded a 180x640 picture carrying it as 640x180,
// the right way up.
func TestOrientationSEIIsTheOneFFmpegHonors(t *testing.T) {
	want := []byte{0, 0, 0, 1, 0x4e, 0x01, 0x2f, 0x03, 0x08, 0x00, 0x14, 0x80}
	if got := orientationSEI(90); !bytes.Equal(got, want) {
		t.Fatalf("orientationSEI(90) = % x, want % x", got, want)
	}
}

func TestOrientationGoesBeforeTheFirstSlice(t *testing.T) {
	vps := []byte{0, 0, 0, 1, 0x40, 0x01, 0xAA}
	sps := []byte{0, 0, 0, 1, 0x42, 0x01, 0xBB}
	pps := []byte{0, 0, 0, 1, 0x44, 0x01, 0xCC}
	idr := []byte{0, 0, 0, 1, 0x26, 0x01, 0xDD}
	var au []byte
	for _, n := range [][]byte{vps, sps, pps, idr} {
		au = append(au, n...)
	}
	got := withOrientation(au, 90)
	var want []byte
	for _, n := range [][]byte{vps, sps, pps, orientationSEI(90), idr} {
		want = append(want, n...)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got  % x\nwant % x", got, want)
	}
}

func TestOrientationLeavesAUnitWithNoSliceAlone(t *testing.T) {
	au := []byte{0, 0, 0, 1, 0x40, 0x01, 0xAA}
	if got := withOrientation(au, 90); !bytes.Equal(got, au) {
		t.Fatalf("changed a unit with no slice: % x", got)
	}
}

func TestRotationForPrefersTheSetting(t *testing.T) {
	zero, ninety := 0, 90
	di := []byte(`<body><DeviceInfo><type>ipc</type><needRotate>1</needRotate></DeviceInfo></body>`)
	if deg, fromCam, _ := rotationFor(Config{Rotate: &zero}, di); deg != 0 || fromCam {
		t.Errorf("rotate = 0 should turn nothing: got %d, fromCamera %v", deg, fromCam)
	}
	if deg, _, _ := rotationFor(Config{Rotate: &ninety}, nil); deg != 90 {
		t.Errorf("rotate = 90 gave %d", deg)
	}
	if deg, fromCam, raw := rotationFor(Config{}, di); deg != 90 || !fromCam || raw != "1" {
		t.Errorf("needRotate 1: got %d %v %q, want 90 from the camera", deg, fromCam, raw)
	}
	plain := []byte(`<body><DeviceInfo><type>ipc</type><needRotate>0</needRotate></DeviceInfo></body>`)
	if deg, _, _ := rotationFor(Config{}, plain); deg != 0 {
		t.Errorf("needRotate 0 gave %d", deg)
	}
}
