package control_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/cgi"
	"github.com/VoltTech21/reostream/internal/control"
	"github.com/VoltTech21/reostream/internal/fakecam"
	"github.com/VoltTech21/reostream/internal/hub"
	"github.com/VoltTech21/reostream/internal/server"
)

// writeTestConfig writes a reostream config naming the given cameras and
// returns its path. Addresses are from the documentation range, never a
// real one, so a test that accidentally dials cannot reach anything. This
// mirrors routes_test.go's own writeTestConfig: that one lives in package
// control, unreachable from here.
//
// The [control] section saying allow_no_password is what makes the install
// CLAIMED, which is the state these pages are reached in. A config with no
// [control] section at all leaves it unclaimed and sends every route to the
// claim screen; see claim.go.
func writeTestConfig(t *testing.T, names ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("listen = \"0.0.0.0:8560\"\n\n[control]\nlisten = \"0.0.0.0:8562\"\nallow_no_password = true\n")
	for i, name := range names {
		fmt.Fprintf(&b, "\n[[camera]]\nname = %q\naddress = \"192.0.2.%d\"\nusername = \"admin\"\npassword = \"\"\nstreams = [\"main\"]\n", name, i+10)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// getBody performs the request through the server and returns the body as
// a string, failing the test on a non-200.
func getBody(t *testing.T, s *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: got %d, want 200: %s", path, resp.StatusCode, body)
	}
	return string(body)
}

// The constants and helpers below (through buildProbeFixture and
// replaceReply) are package control_test's own copies of camprobe_test.go
// and blocks_test.go's fixture builders, needed for the same reason
// writeTestConfig is duplicated in this file: those packages' own copies
// are unexported and this file lives in a different test package
// (control_test, not control), so they are unreachable from here.

const testXMLHeader = `<?xml version="1.0" encoding="UTF-8"?>`
const testProbeNonce = "camerapage-test-nonce-0000000000"
const testProbeDeviceInfo = testXMLHeader + `<body><DeviceInfo><typeInfo>IPC</typeInfo></DeviceInfo></body>`

// buildMessage frames body under h, setting MsgLen from its length the same
// way baichuan's own Writer does.
func buildMessage(h baichuan.Header, body []byte) []byte {
	h.MsgLen = uint32(len(body))
	return append(h.Encode(), body...)
}

// loginHandshake builds the two messages a Conn reads during login: the
// nonce reply, then a login reply carrying deviceInfoXML.
func loginHandshake(nonce, deviceInfoXML string) []byte {
	plainNonce := testXMLHeader + `<body><Encryption version="1.1"><type>md5</type><nonce>` + nonce + `</nonce></Encryption></body>`
	nonceReply := buildMessage(baichuan.Header{
		MsgID: baichuan.MsgIDLogin, Class: baichuan.ClassModern20,
		EncByte: baichuan.NegotiateByte, DirByte: baichuan.DirReply,
	}, baichuan.BCCrypt(0, []byte(plainNonce)))
	loginReply := buildMessage(baichuan.Header{MsgID: baichuan.MsgIDLogin, Class: baichuan.ClassZero},
		baichuan.BCCrypt(0, []byte(deviceInfoXML)))
	return append(nonceReply, loginReply...)
}

// statusReply builds one AES-encrypted post-login reply carrying status.
func statusReply(t *testing.T, key []byte, id uint32, status int16, body string) []byte {
	t.Helper()
	enc, err := baichuan.AESEncrypt(key, []byte(body))
	if err != nil {
		t.Fatalf("encrypt reply %d: %v", id, err)
	}
	return buildMessage(baichuan.Header{
		MsgID:   id,
		Class:   baichuan.ClassZero,
		EncByte: byte(uint16(status) & 0xff),
		DirByte: byte(uint16(status) >> 8),
	}, enc)
}

// buildProbeFixture answers every name baichuan.ConfigNames knows: the
// first with 200, the second with 400, and everything else with 405. A
// full sweep like this is what keeps probeCamera fast in a test: an
// unanswered name costs a real 4 second readTimeout before probeCamera
// gives up on it and reconnects, and baichuan.ConfigNames lists around a
// hundred of them.
func buildProbeFixture(t *testing.T) (fixture []byte, names []string) {
	t.Helper()
	names = baichuan.ConfigNames()
	key := baichuan.AESKey(testProbeNonce, "")
	fixture = loginHandshake(testProbeNonce, testProbeDeviceInfo)
	for i, name := range names {
		id := baichuan.ConfigMessages[name]
		switch i {
		case 0:
			fixture = append(fixture, statusReply(t, key, id, 200, testXMLHeader+"<body/>")...)
		case 1:
			fixture = append(fixture, statusReply(t, key, id, 400, "")...)
		default:
			fixture = append(fixture, statusReply(t, key, id, 405, "")...)
		}
	}
	return fixture, names
}

// replaceReply swaps out the encrypted body of the first reply for id in
// fixture, keeping every other message (and the surrounding message
// framing) untouched. It exists so buildProbeFixture's blanket 405 answer
// for one particular message can be overridden with real content, without
// hand-building the whole fixture a second time.
func replaceReply(t *testing.T, fixture []byte, id uint32, key []byte, newBody string) []byte {
	t.Helper()
	enc, err := baichuan.AESEncrypt(key, []byte(newBody))
	if err != nil {
		t.Fatalf("encrypt replacement body: %v", err)
	}
	off := 0
	for off < len(fixture) {
		h, hn, err := baichuan.DecodeHeader(fixture[off:])
		if err != nil {
			t.Fatalf("decode fixture at %d: %v", off, err)
		}
		end := off + hn + int(h.MsgLen)
		if h.MsgID == id {
			h.EncByte = byte(200)
			h.DirByte = 0
			replaced := buildMessage(h, enc)
			out := append([]byte{}, fixture[:off]...)
			out = append(out, replaced...)
			out = append(out, fixture[end:]...)
			return out
		}
		off = end
	}
	t.Fatalf("fixture has no reply for message %d", id)
	return nil
}

// cgiFloodlightCamera is control_test's own minimal CGI camera double: it
// logs in and answers GetWhiteLed with a fixed mode/state, which is all
// readFloodlight (lights.go) ever asks for on this page. It duplicates the
// shape of lights_test.go's own fakeCGICamera (package control, and so
// unreachable from here) rather than sharing it, for the same reason
// writeTestConfig and noCGIDial are duplicated above.
type cgiFloodlightCamera struct {
	srv *httptest.Server
}

func newCGIFloodlightCamera(t *testing.T, mode, state int) *cgiFloodlightCamera {
	t.Helper()
	c := &cgiFloodlightCamera{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cmd") {
		case "Login":
			w.Write([]byte(`[{"cmd":"Login","code":0,"value":{"Token":{"name":"tok","leaseTime":3600}}}]`))
		case "GetWhiteLed":
			w.Write([]byte(`[{"cmd":"GetWhiteLed","code":0,"value":{"WhiteLed":{"mode":` +
				strconv.Itoa(mode) + `,"state":` + strconv.Itoa(state) + `,"bright":50}}}]`))
		case "GetImage", "GetOsd":
			// The camera page's factory-defaults read. Answered the way a
			// camera reports a command it does not support, which offers
			// no resets and is all these tests need.
			w.Write([]byte(`[{"cmd":"` + r.URL.Query().Get("cmd") + `","code":1,"error":{"detail":"not support","rspCode":-9}}]`))
		// A real camera answers Logout, and every handler that dials a
		// client now hands the session back -- see cgi.Client.Close and
		// the exhaustion it exists to prevent.
		case "Logout":
			w.Write([]byte(`[{"cmd":"Logout","code":0,"value":{"rspCode":200}}]`))
		// The camera page reads the encoder settings on this session too.
		// Answered the way a camera that does not implement it replies,
		// which is what leaves the Encoding section unrendered.
		case "GetEnc":
			w.Write([]byte(`[{"cmd":"GetEnc","code":1,"error":{"detail":"not support","rspCode":-9}}]`))
		// The camera page reads the speaker on this session too. Answered
		// the way a camera without one replies, which is what leaves the
		// Sound section unrendered.
		case "GetAudioCfg", "GetAudioAlarmV20", "GetIsp", "GetAiCfg":
			w.Write([]byte(`[{"cmd":"` + r.URL.Query().Get("cmd") + `","code":1,"error":{"detail":"not support","rspCode":-9}}]`))
		default:
			t.Errorf("cgiFloodlightCamera: unexpected cmd %q", r.URL.Query().Get("cmd"))
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *cgiFloodlightCamera) addr() string { return strings.TrimPrefix(c.srv.URL, "http://") }

// pairGetID finds the message id for a read's firmware description (e.g.
// "osd get"), the same lookup settings.go's own unexported pairForName
// does over baichuan.ConfigPairs, which is unreachable from this package.
// ConfigMessages is keyed by a normalized name ("osd2", not "osd get"), so
// that map is the wrong lookup here.
func pairGetID(t *testing.T, name string) uint32 {
	t.Helper()
	for _, p := range baichuan.ConfigPairs() {
		if p.Name == name {
			return p.Get
		}
	}
	t.Fatalf("no config pair named %q", name)
	return 0
}

// TestCameraLiveTabShowsStreamsAndActions pins the Live tab: the camera's
// stream state, a picker entry for the playable stream with its player URL,
// the shell's Live and Settings tabs, and Remove living under Settings.
func TestCameraLiveTabShowsStreamsAndActions(t *testing.T) {
	h := hub.New(4)
	h.SetKeyframe("H264", nil)
	hubs := server.StaticHubs(map[string]*hub.Hub{"one/sub": h})

	s := newTestServer(t, control.Options{
		AllowNoPassword: true,
		ConfigPath:      writeTestConfig(t, "one"),
		Hubs:            hubs,
		Status: fixedStatus{
			"one/sub": {Connected: true, Streaming: true, FPS: 14.9, BitrateBps: 6.2e5},
		},
	})
	body := getBody(t, s, "/cameras/one")

	for _, want := range []string{
		"streaming",
		`data-stream="sub" data-url="/stream/one_sub.ts"`,
		`href="/cameras/one" aria-current="page">Live</a>`,
		`href="/cameras/one/settings/image">Settings</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Live tab is missing %q:\n%s", want, body)
		}
	}
	// Removing a camera is a setting, not something next to the video.
	if strings.Contains(body, `action="/cameras/one/remove"`) {
		t.Error("the Live tab carries a Remove form; it belongs under Settings")
	}
	rm := getBody(t, s, "/cameras/one/settings/remove")
	if n := strings.Count(rm, `action="/cameras/one/remove"`); n != 1 {
		t.Errorf("%d Remove forms on settings/remove, want 1:\n%s", n, rm)
	}
}

// TestCameraSettingsSectionsReadOnlyTheirOwnBlock pins the Settings tab:
// each category reads its own block and renders the camera's values, and
// the forms send the operator back to that category after a save.
func TestCameraSettingsSectionsReadOnlyTheirOwnBlock(t *testing.T) {
	key := baichuan.AESKey(testProbeNonce, "")
	osdXML := testXMLHeader + `<body><OsdChannelName><channelId>0</channelId><name>lounge</name><enable>1</enable><enWatermark>1</enWatermark><topLeftX>65536</topLeftX><topLeftY>65536</topLeftY></OsdChannelName><OsdDatetime><channelId>0</channelId><enable>1</enable><topLeftX>1</topLeftX><topLeftY>1</topLeftY></OsdDatetime></body>`
	ispXML := testXMLHeader + `<body><VideoInput><channelId>0</channelId><bright>120</bright><contrast>110</contrast><saturation>100</saturation><sharpen>128</sharpen></VideoInput><InputAdvanceCfg><channelId>0</channelId><PowerLineFrequency><mode>60hz</mode><enable>1</enable></PowerLineFrequency><DayNight><mode>auto</mode><IrcutMode>ir</IrcutMode><Threshold>medium</Threshold></DayNight><mirror>0</mirror><flip>0</flip></InputAdvanceCfg></body>`
	ledXML := testXMLHeader + `<body><LedState><channelId>0</channelId><state>auto</state><lightState>open</lightState></LedState></body>`

	for _, tc := range []struct {
		cat, block, doc string
		want            []string
	}{
		{"overlay", "osd get", osdXML, []string{"<h2>Name and timestamp</h2>", `value="lounge"`}},
		{"image", "isp get", ispXML, []string{"Brightness", `class="slider-box" name="value" value="120"`, `type="range"`}},
		{"lights", "led get", ledXML, []string{"Infrared light", "Floodlight", `value="on" checked`}},
	} {
		t.Run(tc.cat, func(t *testing.T) {
			fixture := loginHandshake(testProbeNonce, testProbeDeviceInfo)
			fixture = append(fixture, statusReply(t, key, pairGetID(t, tc.block), 200, tc.doc)...)
			cam := fakecam.New(t, fixture)
			cgiCam := newCGIFloodlightCamera(t, 1, 1) // mode 1, state 1: "on"
			s := newTestServer(t, control.Options{
				AllowNoPassword: true,
				ConfigPath:      writeTestConfig(t, "one"),
				Dial: func(ctx context.Context, c control.Camera) (*baichuan.Conn, error) {
					return baichuan.Dial(ctx, cam.Addr(), baichuan.Options{Password: ""})
				},
				CGIDial: func(c control.Camera) (*cgi.Client, error) {
					return cgi.Dial(cgiCam.addr(), "admin", "")
				},
			})
			body := getBody(t, s, "/cameras/one/settings/"+tc.cat)
			for _, want := range append(tc.want,
				`name="return" value="/cameras/one/settings/`+tc.cat+`"`,
				`aria-current="page">Settings</a>`) {
				if !strings.Contains(body, want) {
					t.Errorf("settings/%s is missing %q:\n%s", tc.cat, want, body)
				}
			}
			if strings.Contains(body, "not available:") {
				t.Errorf("a field that resolved cleanly was rendered as unavailable:\n%s", body)
			}
		})
	}
}
