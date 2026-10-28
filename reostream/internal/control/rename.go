package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// A camera has three names: reostream's (the [[camera]] name, which is in
// every stream URL, so it is kept to lower case, digits, _ and -), the
// device name the Reolink app shows, and the name drawn on the picture.
// Rename sets all three from one field, so they stop drifting apart.

var slugRun = regexp.MustCompile(`[^a-z0-9_-]+`)

// slugName turns what was typed ("Front Door") into reostream's form of it
// ("front_door"). Empty when nothing usable is left.
func slugName(display string) string {
	s := slugRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(display)), "_")
	return strings.Trim(s, "_-")
}

// renameCameraBlock rewrites the name line inside one [[camera]] block and
// nothing else, so the rest of the file stays byte for byte.
func renameCameraBlock(text, from, to string) (string, bool) {
	lines := strings.Split(text, "\n")
	start, end, found := findCameraBlock(lines, from)
	if !found {
		return text, false
	}
	for i := start; i < end; i++ {
		key, _, ok := strings.Cut(strings.TrimSpace(lines[i]), "=")
		if ok && strings.TrimSpace(key) == "name" {
			indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
			lines[i] = fmt.Sprintf("%sname = %q", indent, to)
			return strings.Join(lines, "\n"), true
		}
	}
	return text, false
}

// setCameraText writes one text element of one block on the camera:
// read the block as it is now, change the element, write it back verified.
func (s *Server) setCameraText(ctx context.Context, cam Camera, block, xpath, value string) (WriteResult, error) {
	pair, ok := pairForName(block)
	if !ok {
		return WriteResult{}, fmt.Errorf("%q is not a known block", block)
	}
	conn, err := s.dial(ctx, cam)
	if err != nil {
		return WriteResult{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, readTimeout)
	req := blockRequest(block, conn.Channel())
	doc, status, err := baichuan.ReadConfigWith(readCtx, conn, pair.Get, req)
	cancel()
	conn.Close()
	if err != nil {
		return WriteResult{}, err
	}
	if status != 200 {
		return WriteResult{}, fmt.Errorf("camera answered status %d", status)
	}
	body, err := setField(doc, xpath, value)
	if err != nil {
		return WriteResult{}, err
	}
	return s.writeBlockWith(ctx, cam, pair.Set, body, true, req)
}

// serveRename renames a camera. The camera side goes first, while the
// camera is still reachable under its old name; the config rename after
// it restarts that camera's streams under the new one.
func (s *Server) serveRename(w http.ResponseWriter, r *http.Request) {
	old := r.PathValue("name")
	cam, err := s.byName(old)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	display := strings.TrimSpace(r.PostForm.Get("display"))
	slug := slugName(display)
	back := "/cameras/" + url.PathEscape(old) + "/settings/image"
	if slug == "" {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: "Rename: the name needs at least one letter or digit."})
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	var notes []string
	if r.PostForm.Get("oncamera") != "" {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		targets := []struct{ block, xpath, what string }{
			{"get general", "SystemGeneral/deviceName", "device name"},
			{"osd get", "OsdChannelName/name", "name on the picture"},
		}
		if s.behindRecorder(ctx, cam) {
			// Behind a hub or NVR the device name is the recorder's own;
			// writing it would rename the NVR. The picture's name is per
			// channel, so that one still goes.
			targets = targets[1:]
		}
		for _, t := range targets {
			res, err := s.setCameraText(ctx, cam, t.block, t.xpath, display)
			switch {
			case err != nil:
				notes = append(notes, fmt.Sprintf("the camera's %s was not changed (%v)", t.what, err))
			case res.Outcome == "refused":
				notes = append(notes, fmt.Sprintf("the camera refused its %s", t.what))
			}
		}
		cancel()
	}

	if slug != old {
		if other, err := s.byName(slug); err == nil && other.Name == slug {
			s.setFlash(w, r, Flash{Outcome: "refused", Message: fmt.Sprintf("Rename: there is already a camera named %q.", slug)})
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		text, err := loadRawConfig(s.opts.ConfigPath)
		if err == nil {
			var ok bool
			if text, ok = renameCameraBlock(text, old, slug); !ok {
				err = fmt.Errorf("could not find %q in the config file", old)
			}
		}
		if err == nil {
			_, err = s.writeAndApply(text)
		}
		if err != nil {
			s.setFlash(w, r, Flash{Outcome: "refused", Message: "Rename: " + err.Error()})
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		back = "/cameras/" + url.PathEscape(slug) + "/settings/image"
		notes = append([]string{fmt.Sprintf("its stream URLs are now …/%s, so update Frigate (Settings > Connect to NVR shows the new config)", slug)}, notes...)
	}

	msg := fmt.Sprintf("Renamed to %s.", display)
	if len(notes) > 0 {
		msg += " Note: " + strings.Join(notes, "; ") + "."
	}
	outcome := "confirmed"
	if len(notes) > 0 && slug == old {
		outcome = "refused"
	}
	s.setFlash(w, r, Flash{Outcome: outcome, Message: msg})
	http.Redirect(w, r, back, http.StatusSeeOther)
}
