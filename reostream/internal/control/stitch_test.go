package control

import (
	"html/template"
	"strings"
	"testing"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/cgi"
)

// The capability gate. binoCfg and fishEye are the exact inverse of each
// other on this fleet, which is the physical fact behind them: one sensor
// behind one lens has no seam, two lenses have nothing to dewarp. So the
// two controls must never both appear, and neither must appear on the six
// ordinary cameras -- an alignment control on a single-lens camera offers to
// move a seam that does not exist.
func TestStitchAndFisheyeAreNeverBothOffered(t *testing.T) {
	support := func(fishEye, binoCfg int) baichuan.Support {
		var s baichuan.Support
		id := 0
		s.Support.Item = []baichuan.SupportChannel{{
			ChannelID: &id,
			FishEye:   fishEye,
			BinoCfg:   binoCfg,
		}}
		return s
	}

	for _, c := range []struct {
		name                  string
		fishEye, binoCfg      int
		wantFishEye, wantStit bool
	}{
		// Measured values, not invented ones.
		{"the fisheye", 3, 0, true, false},
		{"the pano", 0, 1, false, true},
		{"an ordinary camera", 0, 0, false, false},
	} {
		s := support(c.fishEye, c.binoCfg)
		if got := s.HasFishEye(0); got != c.wantFishEye {
			t.Errorf("%s: HasFishEye = %v, want %v", c.name, got, c.wantFishEye)
		}
		if got := s.HasStitch(0); got != c.wantStit {
			t.Errorf("%s: HasStitch = %v, want %v", c.name, got, c.wantStit)
		}
		if s.HasFishEye(0) && s.HasStitch(0) {
			t.Errorf("%s: offered both a dewarp and a seam control", c.name)
		}
	}
}

// An out-of-range value is refused with the bound named, using the camera's
// own reported range rather than a constant. A camera that silently clamps
// would leave the page showing a number it is not using.
func TestStitchRefusesValuesTheCameraWouldNotAccept(t *testing.T) {
	// The pano's own reported range.
	var lim cgi.StitchLimits
	lim.Distance.Min, lim.Distance.Max = 2.0, 20.0
	lim.XMove.Min, lim.XMove.Max = -100, 100
	lim.YMove.Min, lim.YMove.Max = -100, 100

	for _, c := range []struct {
		name  string
		in    cgi.Stitch
		want  string
		valid bool
	}{
		{"in range", cgi.Stitch{Distance: 8, XMove: 0, YMove: 0}, "", true},
		{"at both bounds", cgi.Stitch{Distance: 2, XMove: -100, YMove: 100}, "", true},
		{"distance too near", cgi.Stitch{Distance: 1.5}, "2 to 20", false},
		{"distance too far", cgi.Stitch{Distance: 25}, "2 to 20", false},
		{"x too low", cgi.Stitch{Distance: 8, XMove: -101}, "-100 to 100", false},
		{"y too high", cgi.Stitch{Distance: 8, YMove: 101}, "-100 to 100", false},
	} {
		err := checkStitchRange(c.in, lim)
		if c.valid {
			if err != nil {
				t.Errorf("%s: refused a value the camera accepts: %v", c.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted a value outside the camera's range", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal does not name the bound (%q), said: %v", c.name, c.want, err)
		}
	}
}

// A camera that reports no range at all must not have every value refused.
// Some blocks answer with an empty range, and refusing everything would
// make the control useless rather than safe.
func TestStitchAcceptsAnythingWhenTheCameraReportsNoRange(t *testing.T) {
	var none cgi.StitchLimits
	if err := checkStitchRange(cgi.Stitch{Distance: 999, XMove: -999, YMove: 999}, none); err != nil {
		t.Errorf("refused a value although the camera named no range: %v", err)
	}
}

// renderAlign renders align.html through the real layout, the way the
// handler does. Each page defines its own "body", so the page is parsed
// into a fresh clone rather than the shared set -- the same reason
// webui.Renderer does it per request.
func renderAlign(t *testing.T, page alignPage) string {
	t.Helper()
	funcs := templateFuncs(Options{})
	base, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		t.Fatalf("parse layout: %v", err)
	}
	if _, err := base.ParseFS(templateFS, "templates/align.html"); err != nil {
		t.Fatalf("parse align: %v", err)
	}
	var b strings.Builder
	if err := base.ExecuteTemplate(&b, "layout", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// A camera that cannot be read must not produce a page of controls.
//
// This is the failure that prompted the test: the camera ran out of CGI
// session slots, readStitch failed, the error was dropped, and the aligner
// rendered three sliders reading 0 over a range of 0 to 0. Those controls
// write on release, so the first nudge would have sent distance=0 x=0 y=0
// to a camera whose real alignment was no longer on screen to put back.
// Rendering nothing is recoverable; rendering zeros is not.
func TestAlignOffersNoControlsWithoutNumbersFromTheCamera(t *testing.T) {
	for _, c := range []struct {
		name string
		page alignPage
	}{
		{"read failed", alignPage{Camera: "pano", Err: "could not read the lens alignment: max session"}},
		{"never attempted", alignPage{Camera: "pano"}},
	} {
		got := renderAlign(t, c.page)
		for _, id := range []string{`id="al-distance"`, `id="al-x"`, `id="al-y"`, `id="al-revert"`, `id="al-factory"`} {
			if strings.Contains(got, id) {
				t.Errorf("%s: rendered %s over an alignment it never read", c.name, id)
			}
		}
		// The half of the page that needs nothing from the camera stays.
		for _, id := range []string{`id="al-zoom"`, `id="al-guide"`, `id="align-canvas"`} {
			if !strings.Contains(got, id) {
				t.Errorf("%s: dropped %s, which does not depend on the camera", c.name, id)
			}
		}
	}
}

// And the inverse: a good read still produces them, so the test above
// cannot be satisfied by a template that renders nothing at all.
func TestAlignOffersControlsWhenTheCameraAnswered(t *testing.T) {
	page := alignPage{
		Camera: "pano",
		Have:   true,
		Stitch: stitchPage{Distance: 10.5, X: 29, Y: 4, MaxDistance: 20, MaxX: 50, MaxY: 50},
	}
	got := renderAlign(t, page)
	for _, want := range []string{`id="al-distance"`, `id="al-revert"`, `id="al-factory"`, `value="10.5"`, `value="29"`} {
		if !strings.Contains(got, want) {
			t.Errorf("a good read did not render %s", want)
		}
	}
}
