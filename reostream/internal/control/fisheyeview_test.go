package control

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VoltTech21/reostream/internal/hub"
	"github.com/VoltTech21/reostream/internal/server"
)

// The dewarp page's whole claim is that it asks the camera for nothing: it
// plays the stream the dashboard already plays and does the projection in
// the browser. These tests pin the two halves of that -- that the page
// carries a stream to draw from, and that it degrades honestly when there
// is no stream a browser can play -- because a page that silently renders
// an empty canvas looks identical to one that is merely still loading.

func viewServer(t *testing.T, hubs map[string]*hub.Hub) *httptest.Server {
	t.Helper()
	s := newTestServer(t, Options{
		// The config's allow_no_password settles the claim gate; this one
		// settles the session wrapper. Both are needed to reach a page.
		AllowNoPassword: true,
		ConfigPath:      writeTestConfig(t, "fisheye"),
		Hubs:            server.StaticHubs(hubs),
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestTheViewPageCarriesAStreamAndTheRenderer(t *testing.T) {
	// A hub only counts as playable once it has reported a codec a browser
	// can decode; a hub with no keyframe yet reports "" and is skipped.
	sub := hub.New(4)
	sub.SetKeyframe("H264", []byte{0})
	ts := viewServer(t, map[string]*hub.Hub{
		"fisheye/main": hub.New(4),
		"fisheye/sub":  sub,
	})

	res, err := ts.Client().Get(ts.URL + "/cameras/fisheye/view")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("view page answered %d, want 200", res.StatusCode)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	for _, want := range []string{
		`id="dewarp-canvas"`,
		`src="/assets/fisheye.js"`,
		`src="/assets/fisheyeview.js"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("view page is missing %s", want)
		}
	}
	// Without a data-url the renderer has nothing to upload and draws a
	// black rectangle for ever, which is the failure this catches.
	if !strings.Contains(page, `data-url="/stream/`) {
		t.Errorf("view page carries no stream URL:\n%s", page)
	}
}

func TestTheViewPageSaysSoWhenThereIsNothingToDewarp(t *testing.T) {
	// A camera the streaming side knows nothing about has no playable
	// stream, so the page must say that rather than offer a dead canvas.
	ts := viewServer(t, map[string]*hub.Hub{})

	res, err := ts.Client().Get(ts.URL + "/cameras/fisheye/view")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)

	if strings.Contains(page, `id="dewarp-canvas"`) {
		t.Error("view page drew a canvas with no stream behind it")
	}
	if !strings.Contains(page, "nothing to dewarp") {
		t.Errorf("view page does not explain the empty state:\n%s", page)
	}
}

// Dewarping is a crop, so this page wants the biggest stream the browser
// can decode -- the opposite of the dashboard, where every tile plays at
// once and sub is what makes that affordable. A regression here is silent:
// the page still works, it is just soft.
func TestTheViewPagePrefersMainOverSub(t *testing.T) {
	main, sub := hub.New(4), hub.New(4)
	main.SetKeyframe("H264", []byte{0})
	sub.SetKeyframe("H264", []byte{0})
	ts := viewServer(t, map[string]*hub.Hub{"fisheye/main": main, "fisheye/sub": sub})

	res, err := ts.Client().Get(ts.URL + "/cameras/fisheye/view")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if page := string(raw); !strings.Contains(page, `data-url="/stream/fisheye.ts"`) {
		t.Errorf("view page did not choose the main stream; it chose:\n%s",
			between(page, `data-url="`, `"`))
	}
}

// The URL the page hands the renderer has to be one the streaming mount
// will actually serve. Asserting its spelling is not enough: main is a bare
// camera name on that mount, not a "_main" suffix, and the first version of
// this page shipped "/stream/fisheye_main.ts" -- which matched every string
// assertion written about it and 404ed in the browser, leaving a black
// canvas and no error anywhere. So fetch it.
func TestTheStreamURLTheViewPageHandsOutActuallyResolves(t *testing.T) {
	for _, tc := range []struct {
		name   string
		codecs map[string]string
	}{
		{"main is playable", map[string]string{"main": "H264"}},
		{"main is HEVC", map[string]string{"main": "HEVC", "sub": "H264"}},
		{"only extern", map[string]string{"extern": "H264"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hubs := map[string]*hub.Hub{}
			for stream, codec := range tc.codecs {
				h := hub.New(4)
				h.SetKeyframe(codec, []byte{0})
				hubs["fisheye/"+stream] = h
			}
			ts := viewServer(t, hubs)

			res, err := ts.Client().Get(ts.URL + "/cameras/fisheye/view")
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			path := between(string(raw), `data-url="`, `"`)
			if path == "(no stream URL at all)" {
				t.Fatalf("page offered no stream for %v", tc.codecs)
			}

			// A HEAD is enough: it reaches the same lookup a GET does, and
			// a GET on a live stream never returns.
			req, err := http.NewRequest(http.MethodHead, ts.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer got.Body.Close()
			if got.StatusCode == http.StatusNotFound {
				t.Errorf("the page points at %s, which the streaming mount does not serve", path)
			}
		})
	}
}

// An HEVC main stream is not playable in a browser, so the page has to fall
// back rather than offer a canvas nothing will ever paint.
func TestTheViewPageFallsBackWhenMainIsNotPlayable(t *testing.T) {
	main, sub := hub.New(4), hub.New(4)
	main.SetKeyframe("HEVC", []byte{0})
	sub.SetKeyframe("H264", []byte{0})
	ts := viewServer(t, map[string]*hub.Hub{"fisheye/main": main, "fisheye/sub": sub})

	res, err := ts.Client().Get(ts.URL + "/cameras/fisheye/view")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if page := string(raw); !strings.Contains(page, `data-url="/stream/fisheye_sub.ts"`) {
		t.Errorf("view page did not fall back to sub; it chose:\n%s",
			between(page, `data-url="`, `"`))
	}
}

// between pulls the text between two markers, so a failure names the URL
// the page actually chose instead of printing the whole document.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return "(no stream URL at all)"
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return rest
	}
	return rest[:j]
}

func TestTheViewPage404sForAnUnknownCamera(t *testing.T) {
	ts := viewServer(t, map[string]*hub.Hub{"fisheye/main": hub.New(4)})

	res, err := ts.Client().Get(ts.URL + "/cameras/nosuchcamera/view")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("unknown camera answered %d, want 404", res.StatusCode)
	}
}

// The camera-side Image Layout control and this page are different things,
// and the page that offers both has to say which is which. The names below
// were read back off a live camera; an invented name on a control that
// reboots the camera is the bug this pins.
func TestTheFisheyeModesUseTheCamerasOwnNames(t *testing.T) {
	byMode := map[int]string{}
	for _, o := range fisheyeOptions {
		byMode[o.Mode] = o.Value
	}
	// All four were written to a live camera, read back and photographed.
	// Mode 2 is the one a reader will doubt: it is a 2x2 quad that the
	// camera's own menu does not offer, which is why it is pinned here.
	for mode, want := range map[int]string{
		0: "fisheye",
		1: "quad+1",
		2: "quad",
		3: "dual-panoramic",
	} {
		if byMode[mode] != want {
			t.Errorf("mode %d is named %q, want %q -- this one was confirmed against the camera",
				mode, byMode[mode], want)
		}
	}
	for _, o := range fisheyeOptions {
		if o.What == "" {
			t.Errorf("mode %d (%s) has no description", o.Mode, o.Value)
		}
	}
}

// Every control on the dewarp page is a dead control until something reads
// it, and a dead control is invisible: the select still drops down, still
// remembers what you chose, and changes nothing. That is not hypothetical
// drift -- the password page shipped a form whose minlength had come loose
// from the rule behind it, and it looked right in every screenshot.
//
// So: whatever the template puts an id on, the script has to name.
func TestEveryDewarpControlIsReadByTheScript(t *testing.T) {
	page, err := templateFS.ReadFile("templates/view.html")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := assetFS.ReadFile("assets/fisheyeview.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)

	ids := idsIn(string(page))
	if len(ids) < 5 {
		t.Fatalf("found only %v on the dewarp page; the scan is broken, not the page", ids)
	}
	for _, id := range ids {
		// The calibration readouts are the one id the script never spells
		// out: it builds them from the slider's id, so finding the slider
		// and the suffix is finding the readout.
		if strings.HasSuffix(id, "-out") {
			base := strings.TrimSuffix(id, "-out")
			if !strings.Contains(script, "'"+base+"'") || !strings.Contains(script, "'-out'") {
				t.Errorf("the page offers %q and nothing reads it", id)
			}
			continue
		}
		if !strings.Contains(script, "'"+id+"'") {
			t.Errorf("the page offers %q and nothing reads it", id)
		}
	}
}

// The projection select and the renderer have to agree about how many
// projections there are. They live in different files and nothing links
// them, so the failure mode is an option that quietly falls back to
// rectilinear -- which looks exactly like a working control.
func TestTheProjectionSelectAndTheRendererOfferTheSameProjections(t *testing.T) {
	page, err := templateFS.ReadFile("templates/view.html")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := assetFS.ReadFile("assets/fisheye.js")
	if err != nil {
		t.Fatal(err)
	}

	sel := between(string(page), `id="dewarp-projection"`, "</select>")
	offered := strings.Count(sel, "<option ")
	if offered < 2 {
		t.Fatalf("the page offers %d projections; a select with one entry is not a choice", offered)
	}

	// Every projection needs a width to open to, so that list is the
	// renderer's own count of the projections it knows how to draw.
	known := strings.Count(between(string(renderer), "View.defaultZoom = [", "]"), ",") + 1
	if known != offered {
		t.Errorf("the page offers %d projections and the renderer knows %d", offered, known)
	}
	if !strings.Contains(string(renderer), "Renderer.prototype.setProjection") {
		t.Error("the renderer has no setProjection, so the select reaches nothing")
	}
}

// idsIn pulls every id="..." out of a document, in the order they appear.
func idsIn(doc string) []string {
	var ids []string
	rest := doc
	for {
		i := strings.Index(rest, ` id="`)
		if i < 0 {
			return ids
		}
		rest = rest[i+5:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return ids
		}
		ids = append(ids, rest[:j])
		rest = rest[j:]
	}
}

// The mount select's values are not labels, they are arguments: the page
// hands the string straight to the renderer, which decides from it which
// way up the picture goes and which axis the band unrolls around. Rename an
// option value and the renderer quietly treats it as a ceiling -- a picture
// upside down with no error anywhere, which is how the two were found to be
// the wrong way round in the first place.
func TestTheMountValuesAreOnesTheRendererKnows(t *testing.T) {
	page, err := templateFS.ReadFile("templates/view.html")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := assetFS.ReadFile("assets/fisheye.js")
	if err != nil {
		t.Fatal(err)
	}

	values := optionValues(between(string(page), `id="dewarp-mount"`, "</select>"))
	if len(values) != 2 {
		t.Fatalf("the mount select offers %v; the renderer knows two mounts", values)
	}
	for _, v := range values {
		if !strings.Contains(string(renderer), "'"+v+"'") {
			t.Errorf("the page can send mount %q, which the renderer never names", v)
		}
	}
}

// optionValues pulls the value="..." of every <option> in a fragment.
func optionValues(sel string) []string {
	var out []string
	rest := sel
	for {
		i := strings.Index(rest, `<option value="`)
		if i < 0 {
			return out
		}
		rest = rest[i+len(`<option value="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j:]
	}
}
