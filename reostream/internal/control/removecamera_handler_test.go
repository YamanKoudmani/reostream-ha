package control_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VoltTech21/reostream/internal/control"
)

func TestRemoveCameraButtonTakesItOutOfTheConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := `listen = "0.0.0.0:8560"

[control]
listen = "0.0.0.0:8562"
allow_no_password = true

[[camera]]
name = "one"
address = "192.0.2.50"
streams = ["main"]

[[camera]]
name = "two"
address = "192.0.2.51"
streams = ["main"]
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, control.Options{AllowNoPassword: true, ConfigPath: path})

	resp, err := http.Post(ts.URL+"/cameras/one/remove", "application/x-www-form-urlencoded", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("status %d", resp.StatusCode)
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), `name = "one"`) {
		t.Fatalf("camera one still in the config:\n%s\nbody:\n%.2000s", got, body)
	}
}
