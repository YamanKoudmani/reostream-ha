package control

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// serveCopySection copies one curated section's values from this camera to
// the cameras ticked in its Copy panel. Each target's own document is read
// and edited, so whatever else it holds is kept; a setting a target does
// not have is skipped and reported, never invented.
func (s *Server) serveCopySection(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	back := returnTo(r, "/cameras/"+url.PathEscape(cam.Name))
	var group *Group
	for _, g := range groups() {
		if g.Title == r.FormValue("group") {
			g := g
			group = &g
		}
	}
	if group == nil {
		http.Error(w, "unknown section", http.StatusBadRequest)
		return
	}
	targets := r.Form["to"]
	if len(targets) == 0 {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: "Pick at least one camera."})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	src, err := s.dial(ctx, cam)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": could not connect: " + err.Error()})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	_, srcDoc, why := resolveGroupBlock(ctx, src, *group)
	src.Close()
	if why != "" {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": " + why})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	var done, failed []string
	for _, name := range targets {
		if name == cam.Name {
			continue
		}
		if msg := s.copyGroupTo(ctx, *group, srcDoc, name); msg != "" {
			failed = append(failed, name+" ("+msg+")")
		} else {
			done = append(done, name)
		}
	}
	f := Flash{Outcome: "confirmed"}
	switch {
	case len(failed) == 0:
		f.Message = group.Title + " copied to " + strings.Join(done, ", ")
	case len(done) == 0:
		f.Outcome = "refused"
		f.Message = "Not copied: " + strings.Join(failed, "; ")
	default:
		f.Message = "Copied to " + strings.Join(done, ", ") + ". Not copied: " + strings.Join(failed, "; ")
	}
	s.setFlash(w, r, f)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// copyGroupTo writes g's values from srcDoc into the camera named name, and
// says why not when it cannot.
func (s *Server) copyGroupTo(ctx context.Context, g Group, srcDoc []byte, name string) string {
	target, err := s.byName(name)
	if err != nil {
		return "not found"
	}
	conn, err := s.dial(ctx, target)
	if err != nil {
		return "could not connect"
	}
	pair, doc, why := resolveGroupBlock(ctx, conn, g)
	conn.Close()
	if why != "" {
		return "does not have this section"
	}
	body, changed, missing := doc, 0, 0
	for _, f := range g.Fields {
		if f.Kind == "warning" || f.Kind == "reading" || f.NoCopy {
			continue
		}
		for _, path := range f.Paths() {
			v, ok := fieldValue(srcDoc, path)
			if !ok {
				continue
			}
			cur, ok := fieldValue(body, path)
			if !ok {
				missing++
				continue
			}
			if cur == v {
				continue
			}
			next, err := setField(body, path, v)
			if err != nil {
				missing++
				continue
			}
			body = next
			changed++
		}
	}
	if changed == 0 {
		if missing > 0 {
			return "has none of these settings"
		}
		return ""
	}
	res, err := s.writeBlockWith(ctx, target, pair.Set, body, true, blockRequest(g.Blocks[0], target.Channel))
	if err != nil {
		return err.Error()
	}
	if res.Outcome == "refused" {
		return "refused"
	}
	return ""
}
