package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VoltTech21/reostream/internal/config"
)

const savedConfig = `# my notes, keep me
listen = "0.0.0.0:8560"

[rtsp]
listen = "0.0.0.0:8561"

[control]
listen = "0.0.0.0:8562"
password = "correct-horse-battery"

[[camera]]
name = "gate"
address = "192.0.2.10"
username = "admin"
password = "$CAM_PW"
streams = ["main"]
`

func writeSaved(t *testing.T, text string) string {
	t.Helper()
	// The config names an environment variable, which the daemon always has
	// set when it runs; loading it back needs the same.
	t.Setenv("CAM_PW", "secret")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// #9: -save-listen writes the startup flags into an existing config, and
// changes nothing else in it.
func TestSaveListenFlagsUpdatesOnlyTheListenLines(t *testing.T) {
	path := writeSaved(t, savedConfig)
	changed, err := SaveListenFlags(path, "0.0.0.0:9560", "0.0.0.0:9554", "127.0.0.1:9562")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 {
		t.Fatalf("changed = %v, want all three", changed)
	}
	b, _ := os.ReadFile(path)
	want := strings.NewReplacer(`"0.0.0.0:8560"`, `"0.0.0.0:9560"`, `"0.0.0.0:8561"`, `"0.0.0.0:9554"`, `"0.0.0.0:8562"`, `"127.0.0.1:9562"`).Replace(savedConfig)
	if string(b) != want {
		t.Fatalf("file is:\n%s\nwant:\n%s", b, want)
	}
	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:9560" || cfg.RTSP.Listen != "0.0.0.0:9554" || cfg.Control.Listen != "127.0.0.1:9562" {
		t.Fatalf("loaded %q %q %q", cfg.Listen, cfg.RTSP.Listen, cfg.Control.Listen)
	}
}

// A flag that was not given leaves its line alone, and running again with
// the same flags changes nothing.
func TestSaveListenFlagsLeavesUngivenFlagsAlone(t *testing.T) {
	path := writeSaved(t, savedConfig)
	if _, err := SaveListenFlags(path, "", "0.0.0.0:9554", ""); err != nil {
		t.Fatal(err)
	}
	changed, err := SaveListenFlags(path, "", "0.0.0.0:9554", "")
	if err != nil || len(changed) != 0 {
		t.Fatalf("second run changed %v (err %v), want nothing", changed, err)
	}
	b, _ := os.ReadFile(path)
	if want := strings.Replace(savedConfig, `"0.0.0.0:8561"`, `"0.0.0.0:9554"`, 1); string(b) != want {
		t.Fatalf("file is:\n%s", b)
	}
}

// Missing lines and a missing [rtsp] section are added; a missing [control]
// section is not, since one without a password leaves the page unclaimed.
func TestSaveListenFlagsAddsWhatIsMissing(t *testing.T) {
	path := writeSaved(t, "[[camera]]\nname = \"gate\"\naddress = \"192.0.2.10\"\nusername = \"admin\"\npassword = \"x\"\nstreams = [\"main\"]\n")
	changed, err := SaveListenFlags(path, "0.0.0.0:9560", "0.0.0.0:9554", "127.0.0.1:9562")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v, want listen and [rtsp] only", changed)
	}
	cfg, err := config.LoadRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:9560" || cfg.RTSP == nil || cfg.RTSP.Listen != "0.0.0.0:9554" || cfg.Control != nil {
		t.Fatalf("loaded listen=%q rtsp=%+v control=%+v", cfg.Listen, cfg.RTSP, cfg.Control)
	}
	if len(cfg.Cameras) != 1 {
		t.Fatalf("cameras = %d, want the one camera kept", len(cfg.Cameras))
	}
}
