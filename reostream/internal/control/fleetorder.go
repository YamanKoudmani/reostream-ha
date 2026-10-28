package control

import (
	"net/http"
	"sort"
	"strings"

	"github.com/VoltTech21/reostream/internal/config"
)

// The order cameras are shown in, everywhere -- Fleet, the Wall, the
// sidebar -- is the order of their [[camera]] blocks in the config file.
// Arrange on the Fleet page rewrites that order, so one arrangement holds
// for every browser and every page rather than one per browser.

// sortByConfig orders names by where each camera sits in the config file.
// A name the config does not have (a stream still reporting for a camera
// just removed) goes after the configured ones, alphabetically, so the
// page stays stable while it drains away.
func (s *Server) sortByConfig(names []string) {
	pos := map[string]int{}
	if cfg, err := config.Load(s.opts.ConfigPath); err == nil {
		for i, c := range cfg.Cameras {
			pos[c.Name] = i
		}
	}
	sort.SliceStable(names, func(i, j int) bool {
		pi, iok := pos[names[i]]
		pj, jok := pos[names[j]]
		switch {
		case iok && jok:
			return pi < pj
		case iok != jok:
			return iok
		default:
			return names[i] < names[j]
		}
	})
}

// reorderCameraBlocks returns text with its [[camera]] blocks in the order
// names gives. Each block keeps its own text, comments included; only
// which block sits in which slot changes, so everything between blocks
// stays where it was. Cameras names leaves out keep their relative order
// after the ones it lists. It reports false when the file has a block it
// cannot place, and then text comes back unchanged.
func reorderCameraBlocks(text string, current, names []string) (string, bool) {
	lines := strings.Split(text, "\n")
	type span struct{ start, end int }
	spans := make([]span, 0, len(current))
	body := make(map[string]string, len(current))
	for _, name := range current {
		start, end, found := findCameraBlock(lines, name)
		if !found {
			return text, false
		}
		spans = append(spans, span{start, end})
		body[name] = strings.Join(lines[start:end], "\n")
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	want := make([]string, 0, len(current))
	listed := make(map[string]bool, len(names))
	for _, n := range names {
		if _, ok := body[n]; ok && !listed[n] {
			want = append(want, n)
			listed[n] = true
		}
	}
	for _, n := range current {
		if !listed[n] {
			want = append(want, n)
		}
	}

	var b strings.Builder
	prev := 0
	for i, sp := range spans {
		b.WriteString(strings.Join(lines[prev:sp.start], "\n"))
		if sp.start > prev {
			b.WriteString("\n")
		}
		b.WriteString(body[want[i]])
		prev = sp.end
		if prev < len(lines) {
			b.WriteString("\n")
		}
	}
	b.WriteString(strings.Join(lines[prev:], "\n"))
	return b.String(), true
}

// serveFleetOrder saves the order Arrange left the Fleet cards in. The
// body is the camera names in their new order, comma separated. It goes
// through writeAndApply like every other config change; the same cameras
// in a new order reload as unchanged, so no stream restarts.
func (s *Server) serveFleetOrder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	names := strings.Split(r.PostForm.Get("order"), ",")
	cfg, err := config.Load(s.opts.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	current := make([]string, 0, len(cfg.Cameras))
	for _, c := range cfg.Cameras {
		current = append(current, c.Name)
	}
	text, err := loadRawConfig(s.opts.ConfigPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	next, ok := reorderCameraBlocks(text, current, names)
	if !ok {
		http.Error(w, "could not find every camera's block in the config file", http.StatusConflict)
		return
	}
	if next == text {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := s.writeAndApply(next); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
