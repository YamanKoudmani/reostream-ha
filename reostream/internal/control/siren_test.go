package control

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VoltTech21/reostream/internal/cgi"
)

// fakeSpeakerCamera is a CGI camera with a speaker: it answers the two
// commands this firmware actually implements, remembers what was written to
// it, and counts the writes, so a test can say not only what the camera
// holds afterwards but whether it was written to at all. That count is the
// point of the refusal tests: a refused value must not reach a camera.
type fakeSpeakerCamera struct {
	srv    *httptest.Server
	volume int32
	alarm  int32
	sets   int32
}

func newFakeSpeakerCamera(t *testing.T, volume int, alarmOn bool) *fakeSpeakerCamera {
	t.Helper()
	f := &fakeSpeakerCamera{volume: int32(volume)}
	if alarmOn {
		f.alarm = 1
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Query().Get("cmd") {
		case "Login":
			w.Write([]byte(`[{"cmd":"Login","code":0,"value":{"Token":{"name":"tok","leaseTime":3600}}}]`))
		case "Logout":
			w.Write([]byte(`[{"cmd":"Logout","code":0,"value":{"rspCode":200}}]`))
		case "GetAudioCfg":
			w.Write([]byte(`[{"cmd":"GetAudioCfg","code":0,"value":{"AudioCfg":{"volume":` +
				strconv.Itoa(int(atomic.LoadInt32(&f.volume))) +
				`}},"range":{"AudioCfg":{"volume":{"min":0,"max":100}}}}]`))
		case "SetAudioCfg":
			var req []struct {
				Param struct {
					AudioCfg struct {
						Volume int `json:"volume"`
					} `json:"AudioCfg"`
				} `json:"param"`
			}
			if err := json.Unmarshal(body, &req); err != nil || len(req) == 0 {
				t.Errorf("SetAudioCfg body did not parse: %s", body)
			} else {
				atomic.StoreInt32(&f.volume, int32(req[0].Param.AudioCfg.Volume))
			}
			atomic.AddInt32(&f.sets, 1)
			w.Write([]byte(`[{"cmd":"SetAudioCfg","code":0}]`))
		case "GetAudioAlarmV20":
			// The schedule is the point of this reply: it is long, it is
			// model specific, and a write that drops it has thrown away the
			// camera's whole week.
			w.Write([]byte(`[{"cmd":"GetAudioAlarmV20","code":0,"value":{"Audio":{"enable":` +
				strconv.Itoa(int(atomic.LoadInt32(&f.alarm))) +
				`,"schedule":{"channel":0,"table":{"MD":"` + strings.Repeat("1", 168) + `"}}}}}]`))
		case "SetAudioAlarmV20":
			var req []struct {
				Param struct {
					Audio struct {
						Enable   int `json:"enable"`
						Schedule struct {
							Table map[string]string `json:"table"`
						} `json:"schedule"`
					} `json:"Audio"`
				} `json:"param"`
			}
			if err := json.Unmarshal(body, &req); err != nil || len(req) == 0 {
				t.Errorf("SetAudioAlarmV20 body did not parse: %s", body)
			} else {
				if got := req[0].Param.Audio.Schedule.Table["MD"]; len(got) != 168 {
					t.Errorf("SetAudioAlarmV20 sent a schedule of %d hours, not the camera's own 168 -- "+
						"the write composed a document instead of changing the one it read", len(got))
				}
				atomic.StoreInt32(&f.alarm, int32(req[0].Param.Audio.Enable))
			}
			atomic.AddInt32(&f.sets, 1)
			w.Write([]byte(`[{"cmd":"SetAudioAlarmV20","code":0}]`))
		case "GetImage", "GetOsd", "GetEnc", "GetWhiteLed":
			w.Write([]byte(`[{"cmd":"` + r.URL.Query().Get("cmd") + `","code":1,"error":{"detail":"not support","rspCode":-9}}]`))
		default:
			t.Errorf("fakeSpeakerCamera: unexpected cmd %q", r.URL.Query().Get("cmd"))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSpeakerCamera) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// cgiDialTo points the server's CGI dialler at a fake, the same
// substitution lights_test.go makes inline.
func cgiDialTo(addr string) func(Camera) (*cgi.Client, error) {
	return func(Camera) (*cgi.Client, error) { return cgi.Dial(addr, "admin", "") }
}

// postSiren submits the form and stops at the redirect.
//
// Deliberately NOT following it: the redirect lands on the camera page,
// which dials the configured camera over Baichuan and waits out a real
// connect timeout. Following it costs about a minute per call and proves
// nothing this file asks about -- what the camera received is read off the
// fake, which is the only witness that matters here.
func postSiren(t *testing.T, ts *httptest.Server, form url.Values) {
	t.Helper()
	client := *ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.PostForm(ts.URL+"/cameras/fisheye/siren", form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("the siren POST answered %d, want 303", res.StatusCode)
	}
}

func speakerServer(t *testing.T, f *fakeSpeakerCamera) *httptest.Server {
	t.Helper()
	s := newTestServer(t, Options{
		AllowNoPassword: true,
		ConfigPath:      writeTestConfig(t, "fisheye"),
		CGIDial:         cgiDialTo(f.addr()),
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// A volume the camera did not offer is refused with a message naming what it
// does take. The slider's own min and max are not that check: a POST need
// not have come from the slider, and the number box beside it is
// deliberately not clamped to the track.
func TestCheckVolumeNamesWhatTheCameraTakes(t *testing.T) {
	rng := cgi.VolumeRange{Min: 0, Max: 100}
	for _, v := range []int{-1, 101, 1000} {
		err := checkVolume(v, rng)
		if err == nil {
			t.Fatalf("volume %d was allowed; the camera offers %d to %d", v, rng.Min, rng.Max)
		}
		if !strings.Contains(err.Error(), "0 and 100") {
			t.Errorf("refusing %d said %q, which does not say what the camera takes", v, err)
		}
	}
	for _, v := range []int{0, 50, 100} {
		if err := checkVolume(v, rng); err != nil {
			t.Errorf("volume %d is on offer and was refused: %v", v, err)
		}
	}
}

// And the refusal has to happen before anything is sent.
func TestSirenRefusesABadVolumeWithoutTouchingTheCamera(t *testing.T) {
	f := newFakeSpeakerCamera(t, 60, false)
	ts := speakerServer(t, f)

	for _, bad := range []string{"101", "-1", "loud"} {
		postSiren(t, ts, url.Values{"volume": {bad}})
	}
	if n := atomic.LoadInt32(&f.sets); n != 0 {
		t.Errorf("a refused volume still reached the camera %d times", n)
	}
	if v := atomic.LoadInt32(&f.volume); v != 60 {
		t.Errorf("the camera's volume moved to %d on a refused write", v)
	}
}

// The write that should work, end to end: the camera has to receive it.
func TestSirenWritesTheVolumeToTheCamera(t *testing.T) {
	f := newFakeSpeakerCamera(t, 60, false)
	ts := speakerServer(t, f)

	postSiren(t, ts, url.Values{"volume": {"35"}})
	if v := atomic.LoadInt32(&f.volume); v != 35 {
		t.Fatalf("the camera holds volume %d, want 35", v)
	}
}

// Turning the alarm on changes one field of the camera's own document. The
// fake fails the test itself if the 168-hour schedule does not come back,
// because a composed document would silently wipe the camera's week.
func TestSirenTurnsTheAlarmOnWithoutDroppingTheSchedule(t *testing.T) {
	f := newFakeSpeakerCamera(t, 60, false)
	ts := speakerServer(t, f)

	postSiren(t, ts, url.Values{"alarm": {"on"}})
	if on := atomic.LoadInt32(&f.alarm); on != 1 {
		t.Fatalf("the camera's alarm is %d, want 1", on)
	}
}

// A submission that changes nothing must write nothing.
func TestSirenWritesNothingWhenNothingChanged(t *testing.T) {
	f := newFakeSpeakerCamera(t, 60, true)
	ts := speakerServer(t, f)

	postSiren(t, ts, url.Values{"volume": {"60"}, "alarm": {"on"}})
	if n := atomic.LoadInt32(&f.sets); n != 0 {
		t.Errorf("a no-op submission wrote to the camera %d times", n)
	}
}

// Turning the volume down must not also turn the alarm off, or the other way
// round: each field is optional, and an absent one is left alone rather than
// written as zero.
func TestSirenLeavesTheFieldTheFormDidNotCarryAlone(t *testing.T) {
	f := newFakeSpeakerCamera(t, 60, true)
	ts := speakerServer(t, f)

	postSiren(t, ts, url.Values{"volume": {"20"}})
	if on := atomic.LoadInt32(&f.alarm); on != 1 {
		t.Error("writing the volume turned the alarm off")
	}

	postSiren(t, ts, url.Values{"alarm": {"off"}})
	if v := atomic.LoadInt32(&f.volume); v != 20 {
		t.Errorf("writing the alarm moved the volume to %d", v)
	}
}

// readSiren decides whether the section appears at all. A camera that
// refuses the command leaves Have false and the template renders nothing --
// rather than a slider sitting at zero that writes zero when somebody
// submits it, which is the aligner's lesson.
func TestReadSirenOffersNothingWhenTheCameraRefuses(t *testing.T) {
	f := newFakeCGICamera(t, 0, 0) // answers "not support" for the audio commands
	c, err := cgi.Dial(f.addr(), "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	page := readSiren(t.Context(), c, 0)
	if page.Have {
		t.Error("a camera with no speaker was given a volume slider")
	}
	if page.Err == "" {
		t.Error("the refusal was swallowed; nothing says why there is no section")
	}
}
