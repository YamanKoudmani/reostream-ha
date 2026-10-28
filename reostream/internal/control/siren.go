package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/VoltTech21/reostream/internal/cgi"
)

// The camera's own noise.
//
// Two controls, because two is what the camera answers: how loud its speaker
// is, and whether it makes a noise when it sees something. The sound list,
// the custom upload and playing one on demand are all refused by this
// firmware over CGI -- see internal/cgi/audio.go, which records exactly
// which commands were asked and what they said.
//
// Volume is not only the alarm's. It is the speaker's, so it is also how
// loud two-way talk comes out. The page says so, because turning the alarm
// down and finding you cannot be heard through the camera afterwards is a
// bad way to learn it.

// sirenConfirmReason is shown before either write. The same rule as the
// lights: this one is audible in the room, so it confirms every time.
const sirenConfirmReason = ""

// sirenPage is the alarm sound's section on the camera page.
type sirenPage struct {
	// Have says the camera answered. Same discipline as the aligner and the
	// encoder: no controls over a reading that failed, because a zero volume
	// is a plausible-looking number and this form writes.
	Have bool

	Volume    int
	VolumeMin int
	VolumeMax int

	// HaveAlarm is separate from Have because the two commands are
	// separate: a camera may answer the volume and refuse the alarm.
	HaveAlarm bool
	AlarmOn   bool

	Err     string
	Confirm string
}

// readSiren fills the section from the camera, on a session the camera page
// has already opened for the floodlight and the encoder.
func readSiren(ctx context.Context, c *cgi.Client, channel int) sirenPage {
	page := sirenPage{Confirm: sirenConfirmReason}

	cfg, rng, err := c.GetAudioCfg(ctx, channel)
	if err != nil {
		// Not an error on the page. A camera with no speaker says so by
		// refusing the command, and the section simply does not appear --
		// the same positive-answer-only rule the fisheye and stitch use.
		page.Err = fmt.Sprintf("could not read the volume: %v", err)
		return page
	}
	page.Have = true
	page.Volume = cfg.Volume
	page.VolumeMin, page.VolumeMax = rng.Min, rng.Max
	if page.VolumeMax <= page.VolumeMin {
		// A camera that gave a value but no range gets a slider spanning
		// the value alone, which is useless, so give it the full scale
		// these cameras report and let the camera refuse what it dislikes.
		page.VolumeMin, page.VolumeMax = 0, 100
	}

	if doc, err := c.GetAudioAlarm(ctx, channel); err == nil {
		page.HaveAlarm = true
		page.AlarmOn = cgi.AudioAlarmEnabled(doc)
	}
	return page
}

// serveApplySiren writes the volume, the alarm switch, or both.
//
// Both fields are optional, and an absent one is left alone rather than
// written as zero: the form posts only what its own control carries, and a
// silent volume reset is not an acceptable side effect of turning the alarm
// off.
func (s *Server) serveApplySiren(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cam, err := s.byName(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	c, err := s.cgiDial(cam)
	if err != nil {
		s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("could not reach the camera: %v", err))
		return
	}
	defer c.Close()

	cur, rng, err := c.GetAudioCfg(ctx, cam.Channel)
	if err != nil {
		s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("could not read the current volume: %v", err))
		return
	}
	if rng.Max <= rng.Min {
		rng = cgi.VolumeRange{Min: 0, Max: 100}
	}

	var said []string

	if raw := r.FormValue("volume"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("volume must be a whole number, not %q", raw))
			return
		}
		if err := checkVolume(v, rng); err != nil {
			s.sirenOutcome(w, r, name, "refused", err.Error())
			return
		}
		if v != cur.Volume {
			if err := c.SetAudioCfg(ctx, cam.Channel, v); err != nil {
				s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("the camera did not take the volume: %v", err))
				return
			}
			said = append(said, fmt.Sprintf("volume is now %d", v))
		}
	}

	if raw := r.FormValue("alarm"); raw != "" {
		on := raw == "on"
		if !on && raw != "off" {
			s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("%q is not on or off", raw))
			return
		}
		doc, err := c.GetAudioAlarm(ctx, cam.Channel)
		if err != nil {
			s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("could not read the alarm sound: %v", err))
			return
		}
		if cgi.AudioAlarmEnabled(doc) != on {
			if err := c.SetAudioAlarmEnabled(ctx, cam.Channel, doc, on); err != nil {
				s.sirenOutcome(w, r, name, "refused", fmt.Sprintf("the camera did not take the alarm setting: %v", err))
				return
			}
			said = append(said, "the alarm sound is "+raw)
		}
	}

	if len(said) == 0 {
		s.sirenOutcome(w, r, name, "confirmed", "was already set to that, so nothing was written")
		return
	}
	detail := said[0]
	for _, more := range said[1:] {
		detail += "; " + more
	}
	s.sirenOutcome(w, r, name, "confirmed", "saved: "+detail)
}

// checkVolume reports whether the camera said it would take this number.
//
// Checked here and not only by the slider's own min and max, because a POST
// is not obliged to have come from the slider -- and the number box beside
// it is deliberately not clamped to the track, so the page itself can
// submit one the camera never offered.
func checkVolume(v int, rng cgi.VolumeRange) error {
	if v < rng.Min || v > rng.Max {
		return fmt.Errorf("this camera takes a volume between %d and %d, not %d", rng.Min, rng.Max, v)
	}
	return nil
}

// sirenOutcome reports the result the way the encoder's write does, and for
// the same reason: a success here should say what the camera is doing now,
// and a submission that changed nothing must not claim a write it never
// made.
func (s *Server) sirenOutcome(w http.ResponseWriter, r *http.Request, camera, outcome, detail string) {
	if outcome == "confirmed" && detail != "" {
		s.setFlash(w, r, Flash{Outcome: outcome, Message: camera + ": sound " + detail})
	} else {
		s.setFlash(w, r, flashFor(camera+": sound", WriteResult{Outcome: outcome, Detail: detail}))
	}
	http.Redirect(w, r, "/cameras/"+url.PathEscape(camera)+"/settings/audio", http.StatusSeeOther)
}
