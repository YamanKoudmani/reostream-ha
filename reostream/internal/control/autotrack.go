package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/VoltTech21/reostream/internal/cgi"
)

// Auto tracking on a camera that follows what it sees (a TrackMix, say).
// The camera's GetAiCfg answer carries it, read off a TrackMix:
//
//	bSmartTrack          0 or 1, tracking off or on
//	trackType            {people, vehicle, dog_cat, face}: what it follows
//	aiDisappearBackTime  seconds after the target is gone before it returns
//	aiStopBackTime       seconds after the target stops before it returns
//
// with the allowed return times listed in the answer's range. The write is
// SetAiCfg with the same document, the changed fields only.

// trackKind is one thing tracking can follow.
type trackKind struct {
	Key, Label string
	On         bool
}

var trackLabels = []struct{ key, label string }{
	{"people", "People"}, {"vehicle", "Vehicles"}, {"dog_cat", "Animals"},
}

type autoTrackView struct {
	Supported     bool
	Err           string
	On            bool
	Kinds         []trackKind
	DisappearBack int
	StopBack      int
	BackChoices   []int
}

// readAutoTrack returns the view and the camera's own document, which a
// write echoes back.
func readAutoTrack(ctx context.Context, c *cgi.Client, channel int) (autoTrackView, map[string]any, error) {
	var v autoTrackView
	value, _, rng, err := c.Get(ctx, "GetAiCfg", channel)
	if err != nil {
		return v, nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(value, &doc); err != nil {
		return v, nil, err
	}
	on, ok := doc["bSmartTrack"].(float64)
	if !ok {
		return v, doc, nil
	}
	v.Supported, v.On = true, on == 1
	if types, ok := doc["trackType"].(map[string]any); ok {
		for _, l := range trackLabels {
			if n, ok := types[l.key].(float64); ok {
				v.Kinds = append(v.Kinds, trackKind{Key: l.key, Label: l.label, On: n == 1})
			}
		}
	}
	if n, ok := doc["aiDisappearBackTime"].(float64); ok {
		v.DisappearBack = int(n)
	}
	if n, ok := doc["aiStopBackTime"].(float64); ok {
		v.StopBack = int(n)
	}
	var r struct {
		Back []int `json:"aiDisappearBackTime"`
	}
	if json.Unmarshal(rng, &r) == nil {
		v.BackChoices = r.Back
	}
	if len(v.BackChoices) == 0 {
		v.BackChoices = []int{10, 20, 30, 60}
	}
	return v, doc, nil
}

// HasChoice reports whether n is one of the return times on offer, so the
// page can add the camera's own value when it is not.
func (v autoTrackView) HasChoice(n int) bool {
	for _, c := range v.BackChoices {
		if c == n {
			return true
		}
	}
	return false
}

func (s *Server) readAutoTrackView(ctx context.Context, cam Camera) autoTrackView {
	c, err := s.cgiDial(cam)
	if err != nil {
		return autoTrackView{Err: fmt.Sprintf("could not connect: %v", err)}
	}
	defer c.Close()
	v, _, err := readAutoTrack(ctx, c, cam.Channel)
	if err != nil {
		// Most cameras have no tracking at all; only say so when the
		// read failed for some other reason than not having it.
		return autoTrackView{}
	}
	return v
}

// serveApplyAutoTrack changes whichever of tracking on/off, what it
// follows and the two return times the form carries, then reads the
// camera back to confirm.
func (s *Server) serveApplyAutoTrack(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	back := "/cameras/" + url.PathEscape(cam.Name) + "/settings/ptz"
	fail := func(msg string) {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": auto tracking " + msg})
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	c, err := s.cgiDial(cam)
	if err != nil {
		fail("could not connect: " + err.Error())
		return
	}
	defer c.Close()
	_, doc, err := readAutoTrack(ctx, c, cam.Channel)
	if err != nil {
		fail(err.Error())
		return
	}
	if _, ok := doc["bSmartTrack"]; !ok {
		fail("is not something this camera has")
		return
	}

	want := map[string]float64{}
	bit := func(form string) (float64, bool) {
		switch r.FormValue(form) {
		case "1":
			return 1, true
		case "0":
			return 0, true
		}
		return 0, false
	}
	if n, ok := bit("on"); ok {
		doc["bSmartTrack"] = n
		want["bSmartTrack"] = n
	}
	types, _ := doc["trackType"].(map[string]any)
	for _, l := range trackLabels {
		if n, ok := bit(l.key); ok && types != nil {
			if _, has := types[l.key]; has {
				types[l.key] = n
				want["trackType."+l.key] = n
			}
		}
	}
	for _, key := range []string{"aiDisappearBackTime", "aiStopBackTime"} {
		if v := r.FormValue(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				fail(fmt.Sprintf("needs a number of seconds, not %q", v))
				return
			}
			doc[key] = float64(n)
			want[key] = float64(n)
		}
	}
	if len(want) == 0 {
		fail("had nothing to change")
		return
	}
	if err := c.Set(ctx, "SetAiCfg", doc); err != nil {
		fail(err.Error())
		return
	}
	_, after, err := readAutoTrack(ctx, c, cam.Channel)
	if err != nil {
		fail("was sent but could not be read back: " + err.Error())
		return
	}
	afterTypes, _ := after["trackType"].(map[string]any)
	for key, n := range want {
		var got any
		if k, ok := strings.CutPrefix(key, "trackType."); ok {
			got = afterTypes[k]
		} else {
			got = after[key]
		}
		if g, ok := got.(float64); !ok || g != n {
			fail("was accepted but the camera still reports it otherwise")
			return
		}
	}
	s.setFlash(w, r, Flash{Outcome: "confirmed", Message: cam.Name + ": auto tracking saved"})
	http.Redirect(w, r, back, http.StatusSeeOther)
}
