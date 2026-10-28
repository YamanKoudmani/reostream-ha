package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/VoltTech21/reostream/internal/cgi"
)

// The fisheye's view modes.
//
// A fisheye sensor sees a circle. This setting -- the camera's own menu
// calls it "Image Layout" -- decides what the camera ENCODES from that
// circle, and the frame size changes with it: mode 0 is 2560x2560 and mode
// 1 is 3200x3200 on the camera this was measured against.
//
// It is not the setting an operator usually wants. The view modes in the
// vendor's apps -- the draggable quad, the defished band -- are drawn by
// the client from the raw circle and never touch the camera at all (see
// fisheyeview.go, which does the same thing for this page). This one
// re-encodes, so it changes the recording, and it costs a reboot. Reach for
// it to change what is STORED; reach for the view page to change what is
// SEEN.
//
// This is the most expensive write on the page, and the page has to say so
// before anyone presses it:
//
//   - the camera REBOOTS. Its HTTP API answers 502 or nothing for fifteen
//     to twenty seconds, the auth token is invalidated, and every recorder
//     reading it loses the stream and reconnects.
//   - every motion mask and detection zone drawn against the old geometry
//     is now pointing somewhere else. Those coordinates are normalized to
//     the frame, so a zone drawn on the circle is not the same place once
//     the picture is a panorama.
//
// Which is why this is a form with its own confirmation and its own
// warning, rather than another row in a settings group.

// fisheyeOption is one view mode as the page offers it.
type fisheyeOption struct {
	Mode  int
	Value string
	What  string
}

// fisheyeOptions are the modes in the camera's own numbering.
//
// Every one of these was a guess once, and every guess was wrong. They are
// settled now the only way that works: each value written to a live camera,
// read back, and photographed.
//
//	0  the raw circle                  2560x2560
//	1  four panes plus a wide strip    3200x3200
//	2  four panes, 2x2                 2560x2560
//	3  two stacked panoramic bands     2560x2560
//
// Mode 2 is worth calling out. The camera's own menu offers three entries --
// Fisheye View, Quad + 1 View, Dual Panoramic View -- and 2 is not among
// them, yet it is accepted, reads back and encodes a clean 2x2 quad. That
// also settles a contradiction this file used to record: the camera's range
// reports a maximum of 3, meaning four values, while its Support block says
// "3 modes" and the menu lists three. Both are right. Four exist, three are
// offered.
var fisheyeOptions = []fisheyeOption{
	{Mode: 0, Value: "fisheye", What: "the raw circle, uncorrected"},
	{Mode: 1, Value: "quad+1", What: "four dewarped panes and a wide strip; the only mode that encodes 3200x3200"},
	{Mode: 2, Value: "quad", What: "four dewarped panes in a 2x2 grid; the camera's own menu does not offer this one"},
	{Mode: 3, Value: "dual-panoramic", What: "two dewarped halves as stacked bands"},
}

func fisheyeOptionByValue(v string) (fisheyeOption, bool) {
	for _, o := range fisheyeOptions {
		if o.Value == v {
			return o, true
		}
	}
	return fisheyeOption{}, false
}

func fisheyeValueByMode(mode int) string {
	for _, o := range fisheyeOptions {
		if o.Mode == mode {
			return o.Value
		}
	}
	return fmt.Sprintf("mode %d", mode)
}

// fisheyeConfirmReason is what the operator is asked before the write. It
// names both consequences, because neither is recoverable by pressing the
// button again.
const fisheyeConfirmReason = "This restarts the camera (about 20 seconds) and moves its motion zones. Change the view?"

// readFisheye reads the camera's current view mode.
func readFisheye(ctx context.Context, c *cgi.Client, channel int) (cgi.FishEye, error) {
	return c.GetFishEye(ctx, channel)
}

func (s *Server) serveApplyFisheye(w http.ResponseWriter, r *http.Request) {
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
	opt, ok := fisheyeOptionByValue(r.FormValue("option"))
	if !ok {
		http.Error(w, fmt.Sprintf("%q is not a fisheye view", r.FormValue("option")), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	c, err := s.cgiDial(cam)
	if err != nil {
		http.Error(w, fmt.Sprintf("connecting to %s: %v", cam.Name, err), http.StatusBadGateway)
		return
	}
	defer c.Close()

	// The current view, for the undo and for Before. Best effort: a failed
	// pre-read is not a reason to refuse the write.
	var before string
	current, readErr := readFisheye(ctx, c, cam.Channel)
	if readErr == nil {
		before = fisheyeValueByMode(int(current.ImageType))
	}
	if before == opt.Value {
		s.setFlash(w, r, flashFor(cam.Name+": fisheye view", WriteResult{
			Outcome: "accepted",
			Before:  []byte(before),
			Detail:  "the camera is already in that view, so nothing was written and it was not rebooted.",
		}))
		http.Redirect(w, r, "/cameras/"+url.PathEscape(cam.Name)+"/settings/view", http.StatusSeeOther)
		return
	}

	next := current
	next.ImageType = cgi.FishEyeMode(opt.Mode)
	result := WriteResult{Before: []byte(before)}
	if err := c.SetFishEye(ctx, cam.Channel, next); err != nil {
		result.Outcome = "refused"
		result.Detail = err.Error()
	} else {
		// No read-back, deliberately. The camera is rebooting: a read now
		// answers 502, or worse answers from the session that is about to
		// die, and either would be reported as though it said something
		// about the new view. Saying plainly that it is rebooting is more
		// honest than a read-back that cannot mean anything yet.
		result.Outcome = "accepted"
		result.Detail = fmt.Sprintf("the camera took the change to %s and is rebooting. It will be back in about twenty seconds; this page will not know the new view until then. Motion masks and zones on this camera now need redrawing.", opt.Value)
	}

	flash := flashFor(cam.Name+": fisheye view", result)
	// An undo is offered, but it is another reboot, not a cheap revert.
	// The flash says so in Detail above; the button is here because
	// changing the view by accident is worse than changing it back.
	if _, ok := fisheyeOptionByValue(before); ok && result.Outcome != "refused" {
		flash.UndoAction = fmt.Sprintf("/cameras/%s/fisheye", cam.Name)
		flash.UndoParam = "option"
		flash.UndoValue = before
	}
	s.setFlash(w, r, flash)
	http.Redirect(w, r, "/cameras/"+url.PathEscape(cam.Name)+"/settings/view", http.StatusSeeOther)
}

// fisheyeSettleDelay is how long a camera takes to come back from the
// reboot a view change causes. Nothing waits on it: it is here so the
// number the page quotes and the number in the docs are the same one.
const fisheyeSettleDelay = 20 * time.Second
