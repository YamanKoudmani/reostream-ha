package control

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// Changing the password must leave a file TOML can still read.
//
// It did not, on any config where [control] is followed by another table --
// which is every real one, because the cameras come after it. The regexp
// ended `\s*$`, and \s matches a newline, so with (?m) the match ran past
// the end of the password line and ate the blank line after it. The
// replacement put no newline back, so the file came out as
//
//	password = "new"[[camera]]
//
// and the daemon refused its own config with "expected a top-level item to
// end with a newline, comment, or EOF, but got '[' instead". This was hit
// changing the password on a deployed install.
//
// The old tests missed it because a [control] section at the END of a file
// has nothing to run into: the damage is invisible unless something follows.
func TestChangingThePasswordLeavesAParsableConfig(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  string
	}{
		{"camera table follows", `listen = "0.0.0.0:8560"

[control]
listen = "0.0.0.0:8562"
password = "$REOSTREAM_CONTROL_PASSWORD"

[[camera]]
name = "lounge"
address = "192.0.2.29"
`},
		{"control is last", `listen = "0.0.0.0:8560"

[control]
listen = "0.0.0.0:8562"
password = "old"
`},
		{"no blank line before the next table", `[control]
password = "old"
[[camera]]
name = "lounge"
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := setControlPassword(c.raw, "a new password")
			if err != nil {
				t.Fatalf("setControlPassword: %v", err)
			}

			var parsed struct {
				Control struct{ Password string }
				Camera  []struct{ Name string }
			}
			if _, err := toml.Decode(out, &parsed); err != nil {
				t.Fatalf("the rewritten config does not parse: %v\n---\n%s---", err, out)
			}
			if parsed.Control.Password != "a new password" {
				t.Errorf("password = %q, want the new one", parsed.Control.Password)
			}

			// The rest of the file has to survive intact: this edit exists
			// precisely so a camera's $VARIABLE reference is not re-encoded.
			if strings.Contains(c.raw, "[[camera]]") && len(parsed.Camera) != 1 {
				t.Errorf("the camera table did not survive: %d cameras\n---\n%s---", len(parsed.Camera), out)
			}
			if strings.Contains(c.raw, "lounge") && !strings.Contains(out, "lounge") {
				t.Errorf("content after [control] was lost:\n---\n%s---", out)
			}
		})
	}
}
