package control

import (
	"encoding/json"
	"html/template"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// paletteEntry is one line of the "Find a setting" search: what it is
// called, where it lives, and where it goes.
type paletteEntry struct {
	Label string `json:"l"`
	Where string `json:"w"`
	Href  string `json:"h"`
}

// paletteIndex lists every page, every camera's settings sections and every
// curated setting, for the search box on every page. Sections only some
// cameras have (fisheye view, stitching, PTZ) come from the camera's cached
// support block, so the search never dials a camera.
func paletteIndex(configPath string) template.JS {
	var out []paletteEntry
	for _, p := range [][3]string{
		{"Fleet", "reostream", "/"}, {"Wall", "reostream", "/wall"}, {"Logs", "reostream", "/logs"},
		{"Password", "Settings", "/password"}, {"Config file", "Settings", "/config"},
		{"Camera list", "Settings", "/cameras"}, {"Connect to NVR", "Settings", "/settings/nvr"},
		{"About", "Settings", "/settings/about"}, {"Add camera", "Fleet", "/setup"},
	} {
		out = append(out, paletteEntry{p[0], p[1], p[2]})
	}
	cams, _ := loadFleet(configPath)
	// Which section each curated group lives in.
	catOf := map[string]string{}
	for cat, titles := range catGroups {
		for _, t := range titles {
			catOf[t] = cat
		}
	}
	for _, cam := range cams {
		out = append(out, paletteEntry{"Live", cam.Name, "/cameras/" + cam.Name})
		// Only the sections this camera has: a field in a section it
		// lacks (tracking on a camera with no PTZ) led to a 404.
		has := map[string]bool{}
		for _, g := range settingsCats(cachedSupport(cam)) {
			for _, it := range g.items {
				has[it[0]] = true
				out = append(out, paletteEntry{it[1], cam.Name + " · " + g.group, catHref(cam.Name, it[0])})
			}
		}
		for _, g := range groups() {
			cat := catOf[g.Title]
			if cat == "" || !has[cat] {
				continue
			}
			for _, f := range g.Fields {
				if f.Kind == "warning" || f.Label == "" {
					continue
				}
				out = append(out, paletteEntry{f.Label, cam.Name + " · " + g.Title, catHref(cam.Name, cat) + "#row-" + f.Slug})
			}
		}
	}
	b, _ := json.Marshal(out)
	return template.JS(b)
}

// cachedSupport is the camera's support block if a page read it recently,
// nil otherwise.
func cachedSupport(cam Camera) *baichuan.Support {
	identMu.Lock()
	defer identMu.Unlock()
	return identCache[cam.Name+"@"+cam.Address].Support
}
