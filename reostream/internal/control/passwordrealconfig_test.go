package control

import (
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// The same rewrite against the real deployed config's SHAPE.
//
// The fixture in passwordnewline_test.go is a reduction; this is the
// arrangement that actually broke -- eight cameras after [control], each
// with its own password line, two of them $VARIABLE references that this
// edit exists to preserve. If the rewrite ever touches a camera's password
// or drops a table, it shows up here rather than on a live install.
const deployedConfigShape = `# reostream serving the whole fleet.
listen = "0.0.0.0:8560"

[rtsp]
listen = "0.0.0.0:8561"

[control]
listen = "0.0.0.0:8562"
password = "$REOSTREAM_CONTROL_PASSWORD"

[[camera]]
name = "lounge"
address = "192.0.2.29"
username = "admin"
password = ""
streams = ["main", "sub", "extern"]

[[camera]]
name = "fisheye"
address = "192.0.2.11"
username = "admin"
password = "$CAM_PW"
streams = ["main", "sub"]

[[camera]]
name = "pano"
address = "192.0.2.12"
username = "admin"
password = "$CAM_PW"
streams = ["main", "sub", "extern"]
`

func TestTheRewriteLeavesEveryCameraAlone(t *testing.T) {
	out, err := setControlPassword(deployedConfigShape, "a shorter one")
	if err != nil {
		t.Fatalf("setControlPassword: %v", err)
	}

	var parsed struct {
		Control struct{ Password string }
		Camera  []struct {
			Name     string
			Password string
		}
	}
	if _, err := toml.Decode(out, &parsed); err != nil {
		t.Fatalf("the rewritten config does not parse: %v\n---\n%s---", err, out)
	}

	if parsed.Control.Password != "a shorter one" {
		t.Errorf("control password = %q", parsed.Control.Password)
	}
	if len(parsed.Camera) != 3 {
		t.Fatalf("cameras = %d, want 3", len(parsed.Camera))
	}

	// The $VARIABLE references must survive as text. Re-encoding the config
	// once wrote every camera's REAL password into the file in place of
	// these, which is the whole reason this edit works on bytes.
	want := map[string]string{"lounge": "", "fisheye": "$CAM_PW", "pano": "$CAM_PW"}
	for _, c := range parsed.Camera {
		if w, ok := want[c.Name]; !ok {
			t.Errorf("unexpected camera %q", c.Name)
		} else if c.Password != w {
			t.Errorf("camera %s password = %q, want %q", c.Name, c.Password, w)
		}
	}
	if strings.Count(out, "$CAM_PW") != 2 {
		t.Errorf("the $CAM_PW references did not survive verbatim:\n---\n%s---", out)
	}
}

// And against the operator's own file when one is put next to the test,
// which is how this was actually confirmed after the live failure. Skipped
// when absent, so it costs nothing in CI.
func TestTheRewriteHandlesALocalConfigIfOneIsPresent(t *testing.T) {
	raw, err := os.ReadFile("testdata/deployed-config.toml")
	if err != nil {
		t.Skip("no testdata/deployed-config.toml; skipping")
	}
	out, err := setControlPassword(string(raw), "a shorter one")
	if err != nil {
		t.Fatalf("setControlPassword: %v", err)
	}
	if _, err := toml.Decode(out, &struct{}{}); err != nil {
		t.Fatalf("the rewritten config does not parse: %v", err)
	}
}
