package control

import (
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Adding several found cameras in one request, on the page that found them.
//
// The old flow had one button per camera that copied its address up into a
// form at the top and then, on submit, navigated to the camera list -- so
// the sweep's results were gone and the next camera meant sweeping again.
// Eight cameras cost eight sweeps. These pin the replacement.

func addFoundServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := "listen = \"0.0.0.0:8560\"\n\n[control]\nlisten = \"0.0.0.0:8562\"\nallow_no_password = true\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, Options{AllowNoPassword: true, ConfigPath: path})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, path
}

func postAddFound(t *testing.T, ts *httptest.Server, form url.Values) string {
	t.Helper()
	res, err := ts.Client().PostForm(ts.URL+"/setup/add-found", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The point of the change: several cameras, one request, one config.
func TestSeveralFoundCamerasAreAddedInOneRequest(t *testing.T) {
	ts, path := addFoundServer(t)

	page := postAddFound(t, ts, url.Values{
		"add":                {"192.0.2.10", "192.0.2.11"},
		"name.192.0.2.10":    {"front"},
		"name.192.0.2.11":    {"back"},
		"streams.192.0.2.10": {"main", "sub"},
		"streams.192.0.2.11": {"main"},
		"username":           {"admin"},
		"password":           {"hunter2hunter2"},
	})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{`name = "front"`, `name = "back"`, "192.0.2.10", "192.0.2.11"} {
		if !strings.Contains(got, want) {
			t.Errorf("the config is missing %s after adding two cameras:\n%s", want, got)
		}
	}
	if !strings.Contains(page, "Added 2") {
		t.Errorf("the page did not report two additions: %s", page)
	}
}

// A camera with nothing ticked would be added and pull nothing, which is
// the one failure on this page that looks like success. It is refused, and
// refusing it must not discard the rest of the selection.
func TestACameraWithNoStreamsIsRefusedWithoutLosingTheOthers(t *testing.T) {
	ts, path := addFoundServer(t)

	page := postAddFound(t, ts, url.Values{
		"add":                {"192.0.2.10", "192.0.2.11"},
		"name.192.0.2.10":    {"good"},
		"name.192.0.2.11":    {"empty"},
		"streams.192.0.2.10": {"main"},
		"username":           {"admin"},
		"password":           {"hunter2hunter2"},
	})

	raw, _ := os.ReadFile(path)
	got := string(raw)
	if !strings.Contains(got, `name = "good"`) {
		t.Errorf("the valid camera was not added:\n%s", got)
	}
	if strings.Contains(got, `name = "empty"`) {
		t.Errorf("a camera with no streams was added anyway:\n%s", got)
	}
	if !strings.Contains(page, "Added 1") {
		t.Errorf("the page did not report the one that worked: %s", page)
	}
	if !strings.Contains(page, "no streams were ticked") {
		t.Errorf("the page did not say why the other was refused: %s", page)
	}
}

// Ticking nothing is not an error, it is a no-op, and it must say so rather
// than reporting a silent success.
func TestAddingNothingSaysSoRatherThanReportingSuccess(t *testing.T) {
	ts, _ := addFoundServer(t)
	page := postAddFound(t, ts, url.Values{"username": {"admin"}})
	if !strings.Contains(page, "Nothing was ticked") {
		t.Errorf("an empty selection did not say so: %s", page)
	}
	if strings.Contains(page, "Added") {
		t.Errorf("an empty selection reported an addition: %s", page)
	}
}

// The same rule the sweep itself is held to: a credential arrives in the
// request and goes no further. This page renders addresses and names, never
// the password it just used.
func TestAddingFoundCamerasNeverEchoesTheCredential(t *testing.T) {
	ts, _ := addFoundServer(t)
	const secret = "swordfish-swordfish"

	page := postAddFound(t, ts, url.Values{
		"add":                {"192.0.2.10"},
		"name.192.0.2.10":    {"front"},
		"streams.192.0.2.10": {"main"},
		"username":           {"admin"},
		"password":           {secret},
	})
	if strings.Contains(page, secret) {
		t.Errorf("the response carried the password back:\n%s", page)
	}
}

// The name field is pre-filled, not decided: an address makes a poor name
// and a fine default, and it has to be unique within a /24 or two cameras
// arrive wanting the same one.
func TestTheSuggestedNameComesFromTheLastOctet(t *testing.T) {
	for _, c := range []struct{ addr, want string }{
		{"192.0.2.11", "cam-11"},
		{"192.168.1.250", "cam-250"},
		{"10.0.0.7:9000", "cam-7"},
	} {
		if got := suggestedName(c.addr); got != c.want {
			t.Errorf("suggestedName(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}
