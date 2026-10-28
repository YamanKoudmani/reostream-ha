package control_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/VoltTech21/reostream/internal/control"
)

// The talk socket has to come up through the auth wrapper, which only works
// if nothing in that chain hides the connection hijack a WebSocket needs.
// The camera here refuses the connection, so a reached camera dial shows as
// the page's own "could not start talk" status.
func TestTalkSocketReachesTheCameraBehindLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	text := "[control]\nlisten = \"127.0.0.1:0\"\npassword = \"hunter2hunter2xx\"\n\n" +
		"[[camera]]\nname = \"door\"\naddress = \"127.0.0.1:1\"\nstreams = [\"main\"]\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, control.Options{Password: "hunter2hunter2xx", ConfigPath: path})

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.PostForm(ts.URL+"/login", url.Values{"password": {"hunter2hunter2xx"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	u, _ := url.Parse(ts.URL)
	wsURL := "ws://" + u.Host + "/cameras/door/talk?rate=16000"
	d := websocket.Dialer{Jar: jar, HandshakeTimeout: 5 * time.Second}
	ws, _, err := d.Dial(wsURL, http.Header{"Origin": {ts.URL}})
	if err != nil {
		t.Fatalf("dial talk socket: %v", err)
	}
	defer ws.Close()
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(string(msg), "could not start talk") {
		t.Fatalf("status %q", msg)
	}

	// Without the session cookie, no socket.
	if _, _, err := (&websocket.Dialer{}).Dial(wsURL, http.Header{"Origin": {ts.URL}}); err == nil {
		t.Fatal("talk socket opened without logging in")
	}
	// And not from another site's page.
	if _, _, err := d.Dial(wsURL, http.Header{"Origin": {"http://evil.example"}}); err == nil {
		t.Fatal("talk socket opened for a foreign Origin")
	}
}
