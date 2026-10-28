package control

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VoltTech21/reostream/internal/hub"
	"github.com/VoltTech21/reostream/internal/server"
)

func TestTileURLDefaultsToTheSameOriginMount(t *testing.T) {
	// An empty StreamBase must resolve against this page's own /stream/
	// mount, not the browser's current origin left blank: the streaming
	// listener is on a different port and cannot grow CORS headers, so
	// same-origin is the only default that actually plays.
	if got := tileURL("", "/gate_sub.ts"); got != "/stream/gate_sub.ts" {
		t.Fatalf("tileURL(\"\", ...) = %q, want the same-origin /stream/ mount", got)
	}
}

func TestTileURLHonorsAnExplicitBase(t *testing.T) {
	if got := tileURL("http://192.0.2.10:8560", "/gate_sub.ts"); got != "http://192.0.2.10:8560/gate_sub.ts" {
		t.Fatalf("tileURL with an explicit base = %q, want it used verbatim", got)
	}
}

func TestPlayableStreamPrefersSubThenExtern(t *testing.T) {
	cases := []struct {
		name    string
		streams map[string]string
		want    string
		ok      bool
	}{
		{
			name:    "h264 sub is the cheapest thing a browser can play",
			streams: map[string]string{"main": "H265", "sub": "H264", "extern": "H264"},
			want:    "/gate_sub.ts",
			ok:      true,
		},
		{
			// Some models encode the substream in HEVC too, and no browser
			// plays HEVC here. extern is confirmed H.264 at 896x512.
			name:    "hevc sub falls through to extern",
			streams: map[string]string{"main": "H265", "sub": "H265", "extern": "H264"},
			want:    "/gate_extern.ts",
			ok:      true,
		},
		{
			name:    "nothing playable",
			streams: map[string]string{"main": "H265", "sub": "H265"},
			ok:      false,
		},
		{
			// A stream that has never delivered a keyframe reports an empty
			// codec. Guessing it is H.264 would show a broken player rather
			// than an honest "not playable yet".
			name:    "unknown codec is not assumed playable",
			streams: map[string]string{"sub": ""},
			ok:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := playableStream("gate", tc.streams)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A battery camera kept streaming around the clock plays live on its tile;
// one that sleeps shows its last picture and waits to be pressed.
func TestAlwaysOnBatteryTilePlaysLive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`[[camera]]
name = "argus"
address = "192.0.2.10"
uid = "95270000TESTUID1"
password = "x"
always_on = true

[[camera]]
name = "porch"
address = "192.0.2.11"
uid = "95270000TESTUID2"
password = "x"
`), 0o644)
	hubs := map[string]*hub.Hub{}
	for _, n := range []string{"argus/sub", "argus/main", "porch/sub", "porch/main"} {
		h := hub.New(4)
		h.SetOnDemand(true)
		hubs[n] = h
	}
	s := &Server{opts: Options{ConfigPath: path, Hubs: server.StaticHubs(hubs)}}
	got := map[string]tile{}
	for _, tl := range s.tiles() {
		got[tl.Camera] = tl
	}
	if a := got["argus"]; a.Battery || !a.Playable || a.StillNote != "" || a.URL != streamPrefix+"/argus_sub.ts" {
		t.Fatalf("always-on tile %+v, want a live tile", a)
	}
	if p := got["porch"]; !p.Battery {
		t.Fatalf("sleeping battery tile %+v lost its battery mode", p)
	}
}
