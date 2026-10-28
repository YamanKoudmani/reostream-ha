package control

import (
	"strings"
	"testing"
)

func TestMergeFrigateReplacesInputsAndKeepsTheRest(t *testing.T) {
	src := `mqtt:
  host: 192.0.2.5 # broker
cameras:
  lounge:
    ffmpeg:
      hwaccel_args: preset-vaapi
      inputs:
        - path: rtsp://192.0.2.29:554/h264Preview_01_main
          roles: [record]
    detect:
      width: 896
  garage:
    ffmpeg:
      inputs:
        - path: rtsp://192.0.2.40/x
          roles: [detect]
`
	want := map[string][]frigateInput{
		"lounge": {{"http://host:8560/lounge_sub.ts", []string{"detect"}}, {"http://host:8560/lounge.ts", []string{"record", "audio"}}},
		"porch":  {{"http://host:8560/porch.ts", []string{"detect", "record"}}},
	}
	out, updated, added, err := mergeFrigate([]byte(src), want, []string{"lounge", "porch"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, w := range []string{
		"host: 192.0.2.5 # broker",             // comments and other sections survive
		"hwaccel_args: preset-vaapi",           // the camera's other ffmpeg keys survive
		"width: 896",                           // and its other sections
		"path: http://host:8560/lounge_sub.ts", // the new inputs
		"rtsp://192.0.2.40/x",                  // a camera reostream does not have is untouched
		"porch:",                               // a missing camera is added
	} {
		if !strings.Contains(s, w) {
			t.Errorf("merged config is missing %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "h264Preview_01_main") {
		t.Errorf("the old input was not replaced:\n%s", s)
	}
	if len(updated) != 1 || updated[0] != "lounge" || len(added) != 1 || added[0] != "porch" {
		t.Errorf("updated %v added %v", updated, added)
	}
}

func TestMergeFrigateRefusesNonYAML(t *testing.T) {
	if _, _, _, err := mergeFrigate([]byte("cameras: [unclosed"), nil, nil); err == nil {
		t.Fatal("broken YAML was accepted")
	}
}
