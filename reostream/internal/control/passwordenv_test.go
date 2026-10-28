package control

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The README's only [control] example reads the password from
// $REOSTREAM_CONTROL_PASSWORD, so this is not an exotic configuration --
// it is what every install that followed the documentation looks like.
// Refusing to change the password there locked all of them out of their own
// password page, with the advice "change it where it lives", which in a
// container means docker rm and recreate and which the documentation never
// explained. These tests pin the replacement behavior: warn, then obey.

const envPasswordValue = "the-one-from-the-environment"

// envPasswordSession builds an install whose password comes from the
// environment, signed in, the way a real operator reaches this page.
func envPasswordSession(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	t.Setenv("REOSTREAM_CONTROL_PASSWORD", envPasswordValue)

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	raw := "listen = \"0.0.0.0:8560\"\n\n[control]\nlisten = \"0.0.0.0:8562\"\npassword = \"$REOSTREAM_CONTROL_PASSWORD\"\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, Options{ConfigPath: path})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Client{Jar: jar}
	res, err := c.PostForm(ts.URL+"/login", url.Values{"password": {envPasswordValue}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if len(jar.Cookies(mustParse(t, ts.URL))) == 0 {
		t.Fatal("signing in with the environment's password left no session")
	}
	return ts, c, path
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func bodyOf(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Without the acknowledgement the change is refused, and the refusal has to
// name the variable and the file -- an operator cannot act on "there is
// nothing here to change".
func TestAnEnvPasswordIsNotRewrittenWithoutAcknowledgement(t *testing.T) {
	ts, c, path := envPasswordSession(t)

	res, err := c.PostForm(ts.URL+"/password", url.Values{
		"current": {envPasswordValue},
		"new":     {"a-much-longer-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if body := bodyOf(t, res); !strings.Contains(body, "REOSTREAM_CONTROL_PASSWORD") {
		t.Errorf("the warning does not name the variable:\n%s", body)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "$REOSTREAM_CONTROL_PASSWORD") {
		t.Errorf("the config was rewritten without acknowledgement:\n%s", raw)
	}
	if strings.Contains(string(raw), "a-much-longer-one") {
		t.Error("the new password was written into the file anyway")
	}
}

// With it, the change goes through and the file stops referring to the
// variable. Detaching is fine when it is chosen; it was only bad silently.
func TestAnAcknowledgedEnvPasswordIsWrittenIntoTheConfig(t *testing.T) {
	ts, c, path := envPasswordSession(t)

	res, err := c.PostForm(ts.URL+"/password", url.Values{
		"current": {envPasswordValue},
		"new":     {"a-much-longer-one"},
		"detach":  {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := bodyOf(t, res)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, `password = "a-much-longer-one"`) {
		t.Errorf("the new password is not in the config:\n%s\npage said:\n%s", got, body)
	}
	if strings.Contains(got, "$REOSTREAM_CONTROL_PASSWORD") {
		t.Errorf("the config still refers to the variable:\n%s", got)
	}
}

// The page itself has to offer the acknowledgement, or the path above is
// unreachable from a browser and the lockout is unchanged.
func TestThePasswordPageOffersTheAcknowledgement(t *testing.T) {
	ts, c, _ := envPasswordSession(t)

	res, err := c.Get(ts.URL + "/password")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /password answered %d, want 200", res.StatusCode)
	}
	body := bodyOf(t, res)
	for _, want := range []string{`name="detach"`, `name="new"`, "REOSTREAM_CONTROL_PASSWORD"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %s:\n%s", want, body)
		}
	}
}
