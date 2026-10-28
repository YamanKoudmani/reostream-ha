package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// ptzSpeed is the speed the Reolink client uses for a held arrow; the
// range is Reolink's documented 1-64.
const (
	ptzSpeed    = 34
	ptzSpeedMin = 1
	ptzSpeedMax = 64
)

// ptzView is what the Live tab's PTZ card and the Pan, tilt, zoom settings
// show.
type ptzView struct {
	Presets    []baichuan.Preset
	MaxPresets int
	PresetErr  string
	Guard      baichuan.PtzGuard
	GuardErr   string
	// Calibrated is the self test's state: "" unread, else "done" or
	// "not run".
	Calibrated string
	Speed      int
	SpeedMin   int
	SpeedMax   int
}

// NextPresetID is the lowest id no saved preset uses.
func (v ptzView) NextPresetID() int {
	used := map[int]bool{}
	for _, p := range v.Presets {
		used[p.ID] = true
	}
	for i := 0; ; i++ {
		if !used[i] {
			return i
		}
	}
}

func (s *Server) readPresets(ctx context.Context, cam Camera) ptzView {
	v := ptzView{Speed: ptzSpeed, SpeedMin: ptzSpeedMin, SpeedMax: ptzSpeedMax}
	conn, err := s.dial(ctx, cam)
	if err != nil {
		v.PresetErr = fmt.Sprintf("could not connect: %v", err)
		return v
	}
	defer conn.Close()
	if v.Presets, v.MaxPresets, err = conn.PtzPresets(ctx); err != nil {
		v.PresetErr = err.Error()
	}
	return v
}

func (s *Server) readGuard(ctx context.Context, cam Camera) ptzView {
	v := ptzView{}
	conn, err := s.dial(ctx, cam)
	if err != nil {
		v.GuardErr = fmt.Sprintf("could not connect: %v", err)
		return v
	}
	defer conn.Close()
	if v.Guard, err = conn.GetPtzGuard(ctx); err != nil {
		v.GuardErr = err.Error()
	}
	if st, err := conn.PtzCalibrateState(ctx); err == nil {
		v.Calibrated = "not run"
		if st == 2 {
			v.Calibrated = "done"
		}
	}
	return v
}

// ptzCam finds the camera a PTZ command is for and bounds the command,
// writing the failure as the answer.
func (s *Server) ptzCam(w http.ResponseWriter, r *http.Request) (Camera, context.Context, context.CancelFunc, bool) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return Camera{}, nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout*2)
	return cam, ctx, cancel, true
}

// serveApplyPtz moves or stops the camera for the Live tab's pad. It is a
// fetch target, not a form: 204 when the camera acknowledged the command,
// 502 with the reason when it did not.
func (s *Server) serveApplyPtz(w http.ResponseWriter, r *http.Request) {
	cmd := r.FormValue("command")
	switch cmd {
	case "left", "right", "up", "down", "stop":
	default:
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}
	speed := ptzSpeed
	if v, err := strconv.Atoi(r.FormValue("speed")); err == nil {
		speed = min(max(v, ptzSpeedMin), ptzSpeedMax)
	}
	cam, ctx, cancel, ok := s.ptzCam(w, r)
	if !ok {
		return
	}
	defer cancel()
	if err := s.withPtzConn(ctx, cam, func(conn *baichuan.Conn) error {
		return conn.PtzControl(ctx, cmd, speed)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveApplyPreset goes to a saved position, or saves the current one.
// Same fetch contract as serveApplyPtz.
func (s *Server) serveApplyPreset(w http.ResponseWriter, r *http.Request) {
	op := r.FormValue("op")
	cmd := map[string]string{"goto": "toPos", "save": "setPos"}[op]
	if cmd == "" {
		http.Error(w, "unknown preset operation", http.StatusBadRequest)
		return
	}
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil || id < 0 || id > 255 {
		http.Error(w, "bad preset id", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if op == "save" && name == "" {
		name = fmt.Sprintf("Preset %d", id+1)
	}
	if len(name) > 32 {
		name = name[:32]
	}
	cam, ctx, cancel, ok := s.ptzCam(w, r)
	if !ok {
		return
	}
	defer cancel()
	if err := s.withPtzConn(ctx, cam, func(conn *baichuan.Conn) error {
		return conn.PtzPreset(ctx, cmd, id, name)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveApplyGuard sets auto return: on or off, and after how many seconds.
// The camera's own guard document is echoed back with only those two
// fields changed, then read again to confirm.
func (s *Server) serveApplyGuard(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	back := "/cameras/" + url.PathEscape(cam.Name) + "/settings/ptz"
	fail := func(msg string) {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": auto return " + msg})
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		fail("could not connect: " + err.Error())
		return
	}
	defer conn.Close()
	g, err := conn.GetPtzGuard(ctx)
	if err != nil {
		fail(err.Error())
		return
	}
	doc := g.Doc
	enable := g.Enable
	if v := r.FormValue("enable"); v != "" {
		enable = v == "1"
	}
	timeout := g.Timeout
	if v := r.FormValue("timeout"); v != "" {
		t, err := strconv.Atoi(v)
		if err != nil || t < 1 {
			fail(fmt.Sprintf("needs a number of seconds, not %q", v))
			return
		}
		timeout = t
	}
	bit := "0"
	if enable {
		bit = "1"
	}
	if doc, err = setField(doc, "PtzGuard/benable", bit); err == nil {
		doc, err = setField(doc, "PtzGuard/timeout", strconv.Itoa(timeout))
	}
	if err != nil {
		fail(err.Error())
		return
	}
	if err := conn.SetPtzGuard(ctx, doc); err != nil {
		fail(err.Error())
		return
	}
	after, err := conn.GetPtzGuard(ctx)
	if err != nil || after.Enable != enable || after.Timeout != timeout {
		fail("was accepted but the camera still reports it otherwise")
		return
	}
	state := "off"
	if enable {
		state = fmt.Sprintf("on, after %d s", timeout)
	}
	s.setFlash(w, r, Flash{Outcome: "confirmed", Message: cam.Name + ": auto return " + state})
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// serveCalibrate starts the camera's pan/tilt self test: it moves through
// its whole range to find its limits, as the Reolink client's Calibrate.
func (s *Server) serveCalibrate(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	back := "/cameras/" + url.PathEscape(cam.Name) + "/settings/ptz"
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err == nil {
		err = conn.PtzCalibrate(ctx)
		conn.Close()
	}
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": calibrate " + err.Error()})
	} else {
		s.setFlash(w, r, Flash{Outcome: "accepted", Message: cam.Name + ": calibrating, the camera moves through its whole range for a minute or two"})
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// serveGuardPosition saves where the camera points now as its guard
// position (op=set), or sends it there (op=go). Over the HTTP API:
// SetPtzGuard's cmdStr setPos / toPos, as the camera's own web server
// names them; the Baichuan guard message has no such command.
func (s *Server) serveGuardPosition(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	back := "/cameras/" + url.PathEscape(cam.Name) + "/settings/ptz"
	op := r.FormValue("op")
	param := map[string]any{"channel": cam.Channel}
	what := ""
	switch op {
	case "set":
		param["cmdStr"], param["bSaveCurrentPos"] = "setPos", 1
		what = "guard position saved where it points now"
	case "go":
		param["cmdStr"] = "toPos"
		what = "going to the guard position"
	default:
		http.Error(w, "op is set or go", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	c, err := s.cgiDial(cam)
	if err == nil {
		defer c.Close()
		// Keep the camera's own enable and timeout in the same write.
		if value, _, _, gerr := c.Get(ctx, "GetPtzGuard", cam.Channel); gerr == nil {
			var cur struct {
				PtzGuard map[string]any `json:"PtzGuard"`
			}
			if json.Unmarshal(value, &cur) == nil {
				for _, k := range []string{"benable", "timeout"} {
					if v, ok := cur.PtzGuard[k]; ok {
						param[k] = v
					}
				}
			}
		}
		err = c.Set(ctx, "SetPtzGuard", map[string]any{"PtzGuard": param})
		c.Close()
	}
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: cam.Name + ": " + err.Error()})
	} else {
		s.setFlash(w, r, Flash{Outcome: "accepted", Message: cam.Name + ": " + what})
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
