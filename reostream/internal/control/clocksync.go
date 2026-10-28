package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/VoltTech21/reostream/internal/cgi"
	"github.com/VoltTech21/reostream/internal/config"
)

// Setting a camera's clock from this machine's: "Sync now" on a camera's
// Date and time page, and once a day for every wired camera when the
// config's [clock] daily_sync is set.
//
// The camera keeps local time: year..sec, with timeZone in seconds WEST of
// UTC and isDst saying whether its daylight saving offset (Dst.offset,
// hours) applies now. Read off a TrackMix in Chicago in October:
// timeZone 21600, isDst 1, offset 1, hour 10 at 15:48 UTC.

// cameraClock is a camera's clock as it reads it, and the offset it keeps
// local time at.
type cameraClock struct {
	doc    map[string]any // the Time object, echoed back on write
	local  time.Time      // its year..sec, as written
	offset time.Duration  // local minus UTC
}

func readCameraClock(ctx context.Context, c *cgi.Client) (cameraClock, error) {
	value, _, _, err := c.Get(ctx, "GetTime", 0)
	if err != nil {
		return cameraClock{}, err
	}
	var v struct {
		Time map[string]any `json:"Time"`
		Dst  map[string]any `json:"Dst"`
	}
	if err := json.Unmarshal(value, &v); err != nil || v.Time == nil {
		return cameraClock{}, fmt.Errorf("control: parsing GetTime: %v", err)
	}
	n := func(m map[string]any, k string) int {
		f, _ := m[k].(float64)
		return int(f)
	}
	cc := cameraClock{doc: v.Time}
	cc.local = time.Date(n(v.Time, "year"), time.Month(n(v.Time, "mon")), n(v.Time, "day"),
		n(v.Time, "hour"), n(v.Time, "min"), n(v.Time, "sec"), 0, time.UTC)
	cc.offset = -time.Duration(n(v.Time, "timeZone")) * time.Second
	if v.Dst != nil && dstNow(v.Time, v.Dst, time.Now(), cc.offset) {
		cc.offset += time.Duration(n(v.Dst, "offset")) * time.Hour
	}
	return cc, nil
}

// dstNow reports whether the camera keeps daylight saving now. Newer
// firmware says so (Time.isDst); older firmware (an RLC-810A on
// v3.1.0.956) leaves that out and only sends its rules, which are then
// applied here the way the camera applies them: from the startWeek-th
// startWeekday of startMon at startHour, standard time, to the endWeek-th
// endWeekday of endMon at endHour, daylight time; week 5 is the last.
func dstNow(tm, dst map[string]any, now time.Time, standard time.Duration) bool {
	n := func(m map[string]any, k string) (int, bool) {
		f, ok := m[k].(float64)
		return int(f), ok
	}
	if v, ok := n(tm, "isDst"); ok {
		return v == 1
	}
	if on, _ := n(dst, "enable"); on != 1 {
		return false
	}
	g := func(k string) int { v, _ := n(dst, k); return v }
	local := now.UTC().Add(standard) // standard local time, as UTC fields
	start := nthWeekday(local.Year(), time.Month(g("startMon")), g("startWeek"), time.Weekday(g("startWeekday"))).
		Add(time.Duration(g("startHour"))*time.Hour + time.Duration(g("startMin"))*time.Minute + time.Duration(g("startSec"))*time.Second)
	end := nthWeekday(local.Year(), time.Month(g("endMon")), g("endWeek"), time.Weekday(g("endWeekday"))).
		Add(time.Duration(g("endHour"))*time.Hour + time.Duration(g("endMin"))*time.Minute + time.Duration(g("endSec"))*time.Second).
		Add(-time.Duration(g("offset")) * time.Hour) // the end is given in daylight time
	if start.Before(end) {
		return !local.Before(start) && local.Before(end)
	}
	return !local.Before(start) || local.Before(end) // southern hemisphere
}

// nthWeekday is the n-th wd of month in year (n 5 is the last), at
// midnight, as a UTC-fields time.
func nthWeekday(year int, month time.Month, n int, wd time.Weekday) time.Time {
	if n >= 5 {
		last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
		return last.AddDate(0, 0, -((int(last.Weekday()) - int(wd) + 7) % 7))
	}
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 0, (int(wd)-int(first.Weekday())+7)%7+7*(n-1))
}

// drift is how far the camera's clock is from now: positive is fast.
func (cc cameraClock) drift(now time.Time) time.Duration {
	want := now.UTC().Add(cc.offset)
	want = time.Date(want.Year(), want.Month(), want.Day(), want.Hour(), want.Minute(), want.Second(), 0, time.UTC)
	return cc.local.Sub(want)
}

// driftText is drift for a person: "in sync", "2 min 10 s fast".
func driftText(d time.Duration) string {
	switch {
	case d > -3*time.Second && d < 3*time.Second:
		return "in sync"
	case d < 0:
		return durationWords(-d) + " slow"
	}
	return durationWords(d) + " fast"
}

func durationWords(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, sec := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case m > 0:
		return fmt.Sprintf("%d min %d s", m, sec)
	}
	return fmt.Sprintf("%d s", sec)
}

// syncClock sets cam's clock from this machine's, on top of the camera's own
// Time document, and reads it back.
func (s *Server) syncClock(ctx context.Context, cam Camera) (WriteResult, error) {
	c, err := s.cgiDial(cam)
	if err != nil {
		return WriteResult{}, fmt.Errorf("control: connecting to %q for its clock: %w", cam.Name, err)
	}
	defer c.Close()
	cur, err := readCameraClock(ctx, c)
	if err != nil {
		return WriteResult{}, fmt.Errorf("control: reading %q's clock: %w", cam.Name, err)
	}
	result := WriteResult{Before: []byte(driftText(cur.drift(time.Now())))}
	next := make(map[string]any, len(cur.doc))
	for k, v := range cur.doc {
		next[k] = v
	}
	local := time.Now().UTC().Add(cur.offset)
	next["year"], next["mon"], next["day"] = local.Year(), int(local.Month()), local.Day()
	next["hour"], next["min"], next["sec"] = local.Hour(), local.Minute(), local.Second()
	if err := c.Set(ctx, "SetTime", map[string]any{"Time": next}); err != nil {
		result.Outcome, result.Detail = "refused", err.Error()
		return result, nil
	}
	after, err := readCameraClock(ctx, c)
	if err != nil {
		result.Outcome = "accepted"
		result.Detail = fmt.Sprintf("the camera took the time, but reading it back failed: %v", err)
		return result, nil
	}
	d := after.drift(time.Now())
	result.After = []byte(driftText(d))
	if d > -3*time.Second && d < 3*time.Second {
		result.Outcome = "confirmed"
	} else {
		result.Outcome = "accepted"
		result.Detail = "the camera took the time but still reads " + driftText(d)
	}
	return result, nil
}

// serveSyncClock is "Sync now": this camera, or every camera when ticked.
func (s *Server) serveSyncClock(w http.ResponseWriter, r *http.Request) {
	cam, ok := s.timeCamera(w, r)
	if !ok {
		return
	}
	if applyEveryCamera(r) {
		results := s.applyAll(r.Context(), func(ctx context.Context, c Camera) (WriteResult, error) {
			return s.syncClock(ctx, c)
		})
		s.redirectFleetResults(w, r, cam, "clock", results)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	result, err := s.syncClock(ctx, cam)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: err.Error()})
	} else {
		s.setFlash(w, r, flashFor(cam.Name+": clock", result))
	}
	http.Redirect(w, r, "/cameras/"+cam.Name+"/time", http.StatusSeeOther)
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// serveDailySync saves the fleet's daily sync time ("" turns it off).
func (s *Server) serveDailySync(w http.ResponseWriter, r *http.Request) {
	cam, ok := s.timeCamera(w, r)
	if !ok {
		return
	}
	at := strings.TrimSpace(r.FormValue("daily_sync"))
	if at != "" && !hhmm.MatchString(at) {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: "daily sync wants a time like 03:00"})
		http.Redirect(w, r, "/cameras/"+cam.Name+"/time", http.StatusSeeOther)
		return
	}
	section := "[clock]\n"
	if at != "" {
		section += fmt.Sprintf("daily_sync = %q\n", at)
	}
	text, err := loadRawConfig(s.opts.ConfigPath)
	if err == nil {
		_, err = s.writeAndApply(upsertSection(text, "clock", section))
	}
	switch {
	case err != nil:
		s.setFlash(w, r, Flash{Outcome: "refused", Message: "Not saved: " + err.Error()})
	case at == "":
		s.setFlash(w, r, Flash{Outcome: "confirmed", Message: "Daily clock sync is off"})
	default:
		s.setFlash(w, r, Flash{Outcome: "confirmed", Message: "Every camera's clock is synced daily at " + at})
	}
	http.Redirect(w, r, "/cameras/"+cam.Name+"/time", http.StatusSeeOther)
}

// dailySync remembers the last daily run, for the page.
var dailySync struct {
	mu      sync.Mutex
	day     string
	summary string
}

// clockScheduler runs the daily sync. Battery cameras are left out: the
// sync would wake each one every day.
func (s *Server) clockScheduler(ctx context.Context) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			cfg, err := config.Load(s.opts.ConfigPath)
			if err != nil || cfg.Clock == nil || cfg.Clock.DailySync == "" || now.Format("15:04") != cfg.Clock.DailySync {
				continue
			}
			today := now.Format("2006-01-02")
			dailySync.mu.Lock()
			done := dailySync.day == today
			dailySync.day = today
			dailySync.mu.Unlock()
			if done {
				continue
			}
			s.runDailySync(ctx, now)
		}
	}
}

func (s *Server) runDailySync(ctx context.Context, now time.Time) {
	cams, err := s.fleet()
	if err != nil {
		return
	}
	ok, total := 0, 0
	var failed []string
	for _, cam := range cams {
		if cam.UID != "" || s.behindRecorder(ctx, cam) {
			continue
		}
		total++
		cctx, cancel := context.WithTimeout(ctx, fleetApplyTimeout)
		res, err := s.syncClock(cctx, cam)
		cancel()
		if err == nil && res.Outcome == "confirmed" {
			ok++
		} else {
			failed = append(failed, cam.Name)
		}
	}
	summary := fmt.Sprintf("%s: %d of %d cameras synced", now.Format("Jan 2 15:04"), ok, total)
	if len(failed) > 0 {
		summary += " (not: " + strings.Join(failed, ", ") + ")"
	}
	log.Printf("control: daily clock sync %s", summary)
	dailySync.mu.Lock()
	dailySync.summary = summary
	dailySync.mu.Unlock()
}

// setTimeField sets one field of the camera's Time document (hourFmt: 1
// is 12 hour, the "am"/"pm" in its timestamp, 0 is 24 hour; timeFmt: one
// of timeFormats) on top of the rest of it, and reads it back.
func (s *Server) setTimeField(ctx context.Context, cam Camera, key string, want any) (WriteResult, error) {
	c, err := s.cgiDial(cam)
	if err != nil {
		return WriteResult{}, fmt.Errorf("control: connecting to %q for its clock: %w", cam.Name, err)
	}
	defer c.Close()
	cur, err := readFullTime(ctx, c)
	if err != nil {
		return WriteResult{}, fmt.Errorf("control: reading %q's clock: %w", cam.Name, err)
	}
	result := WriteResult{Before: []byte(fmt.Sprint(cur[key]))}
	next := make(map[string]any, len(cur))
	for k, v := range cur {
		next[k] = v
	}
	next[key] = want
	if err := c.Set(ctx, "SetTime", map[string]any{"Time": next}); err != nil {
		result.Outcome, result.Detail = "refused", err.Error()
		return result, nil
	}
	after, err := readFullTime(ctx, c)
	if err != nil {
		result.Outcome, result.Detail = "accepted", "reading it back failed: "+err.Error()
		return result, nil
	}
	result.After = []byte(fmt.Sprint(after[key]))
	if fmt.Sprint(after[key]) == fmt.Sprint(want) {
		result.Outcome = "confirmed"
	} else {
		result.Outcome, result.Detail = "accepted", "the camera still reads "+fmt.Sprint(after[key])
	}
	return result, nil
}

// timeFormats are the date formats the camera offers (its GetTime range).
var timeFormats = []string{"MM/DD/YYYY", "YYYY/MM/DD", "DD/MM/YYYY"}

// serveHourFormat is the 12/24 hour switch, this camera or all of them.
func (s *Server) serveHourFormat(w http.ResponseWriter, r *http.Request) {
	cam, ok := s.timeCamera(w, r)
	if !ok {
		return
	}
	hourFmt := 0
	if r.FormValue("value") == "12" {
		hourFmt = 1
	}
	if applyEveryCamera(r) {
		results := s.applyAll(r.Context(), func(ctx context.Context, c Camera) (WriteResult, error) {
			return s.setTimeField(ctx, c, "hourFmt", hourFmt)
		})
		s.redirectFleetResults(w, r, cam, "hour format", results)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	result, err := s.setTimeField(ctx, cam, "hourFmt", hourFmt)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: err.Error()})
	} else {
		s.setFlash(w, r, flashFor(cam.Name+": clock format", result))
	}
	http.Redirect(w, r, "/cameras/"+cam.Name+"/time", http.StatusSeeOther)
}

// serveDateFormat is the date format choice, this camera or all of them.
func (s *Server) serveDateFormat(w http.ResponseWriter, r *http.Request) {
	cam, ok := s.timeCamera(w, r)
	if !ok {
		return
	}
	want := r.FormValue("value")
	if !slices.Contains(timeFormats, want) {
		http.Error(w, "unknown date format", http.StatusBadRequest)
		return
	}
	if applyEveryCamera(r) {
		results := s.applyAll(r.Context(), func(ctx context.Context, c Camera) (WriteResult, error) {
			return s.setTimeField(ctx, c, "timeFmt", want)
		})
		s.redirectFleetResults(w, r, cam, "date format", results)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	result, err := s.setTimeField(ctx, cam, "timeFmt", want)
	if err != nil {
		s.setFlash(w, r, Flash{Outcome: "refused", Message: err.Error()})
	} else {
		s.setFlash(w, r, flashFor(cam.Name+": date format", result))
	}
	http.Redirect(w, r, "/cameras/"+cam.Name+"/time", http.StatusSeeOther)
}
