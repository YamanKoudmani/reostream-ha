package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Privacy mode (E1 series): the camera stops its video, audio and controls
// until it is turned off. Read with get sleep state cfg (574), set with set
// sleep state cfg (575) carrying operate 2 and sleep 1 or 0, as the Reolink
// app does (reocapture log on issue #13, 2026-10-25). Some cameras answer
// the read but refuse the write (a TrackMix answers 400); those are
// remembered and the switch is not offered again.

const (
	msgGetSleepState = 574
	msgSetSleepState = 575
)

var noPrivacyMode sync.Map // camera name -> true once a write was refused

type privacyModeView struct{ On bool }

func (s *Server) readPrivacyMode(ctx context.Context, conn *baichuan.Conn, cam string, page *cameraPage) {
	if _, refused := noPrivacyMode.Load(cam); refused {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, msgGetSleepState)
	cancel()
	if err != nil || status != 200 {
		return
	}
	v, ok := fieldValue(doc, "sleepState/sleep")
	if !ok {
		return
	}
	page.PrivacyMode = &privacyModeView{On: strings.TrimSpace(v) == "1"}
}

func sleepStateDoc(on bool) []byte {
	sleep := 0
	if on {
		sleep = 1
	}
	return []byte(fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<sleepState version=\"1.1\">\n<operate>2</operate>\n<sleep>%d</sleep>\n</sleepState>\n</body>\n", sleep))
}

// privacyWaker is the supervisor's PrivacyOff.
type privacyWaker interface{ PrivacyOff(camera string) }

func (s *Server) servePrivacyMode(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	on := r.FormValue("value") == "1"
	back := returnTo(r, "/cameras/"+url.PathEscape(cam.Name))
	word := map[bool]string{true: "on", false: "off"}[on]
	subject := cam.Name + ": privacy mode " + word
	flash := func(res WriteResult) {
		s.setFlash(w, r, flashFor(subject, res))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	status, err := baichuan.WriteConfig(ctx, conn, msgSetSleepState, sleepStateDoc(on))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if status != 200 {
		if status == 400 || status == 405 {
			noPrivacyMode.Store(cam.Name, true)
		}
		flash(WriteResult{Outcome: "refused", Detail: "this camera does not take privacy mode (" + baichuan.ExplainStatus(status) + ")"})
		return
	}
	doc, st, err := baichuan.ReadConfig(ctx, conn, msgGetSleepState)
	v, _ := fieldValue(doc, "sleepState/sleep")
	if err != nil || st != 200 || (strings.TrimSpace(v) == "1") != on {
		flash(WriteResult{Outcome: "accepted", Detail: "the camera answered OK but did not read back " + word})
		return
	}
	if !on {
		if pw, ok := s.opts.Supervisor.(privacyWaker); ok {
			pw.PrivacyOff(cam.Name)
		}
	}
	flash(WriteResult{Outcome: "confirmed", Detail: "privacy mode is " + word})
}
