package control_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/control"
	"github.com/VoltTech21/reostream/internal/fakecam"
)

// The privacy mode switch shows on a camera that answers the sleep state
// read; a camera that then refuses the write (a TrackMix answers 400) is
// remembered and not offered the switch again.
func TestPrivacyModeSwitchShowsAndHidesAfterARefusal(t *testing.T) {
	key := baichuan.AESKey(testProbeNonce, "")
	sleepState := testXMLHeader + `<body><sleepState version="1.1"><sleep>0</sleep><mode>1</mode></sleepState></body>`
	page := loginHandshake(testProbeNonce, testProbeDeviceInfo)
	page = append(page, statusReply(t, key, 574, 200, sleepState)...)
	page = append(page, statusReply(t, key, 52, 405, "")...)
	refuse := loginHandshake(testProbeNonce, testProbeDeviceInfo)
	refuse = append(refuse, statusReply(t, key, 575, 400, "")...)

	pageCam, refuseCam := fakecam.New(t, page), fakecam.New(t, refuse)
	var writing atomic.Bool
	s := newTestServer(t, control.Options{
		AllowNoPassword: true,
		ConfigPath:      writeTestConfig(t, "one"),
		Dial: func(ctx context.Context, c control.Camera) (*baichuan.Conn, error) {
			addr := pageCam.Addr()
			if writing.Load() {
				addr = refuseCam.Addr()
			}
			return baichuan.Dial(ctx, addr, baichuan.Options{Password: ""})
		},
	})
	if body := getBody(t, s, "/cameras/one/settings/privacy"); !strings.Contains(body, "<h2>Privacy mode</h2>") {
		t.Fatalf("no privacy mode switch on a camera that answers the read:\n%s", body)
	}
	writing.Store(true)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(s.URL+"/cameras/one/privacymode", url.Values{"value": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST status %d", resp.StatusCode)
	}
	writing.Store(false)
	if body := getBody(t, s, "/cameras/one/settings/privacy"); strings.Contains(body, "<h2>Privacy mode</h2>") {
		t.Fatal("the switch is still offered after the camera refused privacy mode")
	}
}
