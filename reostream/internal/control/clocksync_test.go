package control

import (
	"encoding/json"
	"testing"
	"time"
)

// The camera's own example: a TrackMix in Chicago in October reads
// timeZone 21600 (seconds west), isDst 1, Dst.offset 1, and shows 10:48:14
// at 15:48:14 UTC. That is in sync; a minute later it is 1 min slow.
func TestCameraClockOffsetAndDrift(t *testing.T) {
	var v struct {
		Time map[string]any `json:"Time"`
		Dst  map[string]any `json:"Dst"`
	}
	json.Unmarshal([]byte(`{"Time":{"year":2026,"mon":10,"day":24,"hour":10,"min":48,"sec":14,"timeZone":21600,"isDst":1},"Dst":{"offset":1}}`), &v)
	n := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	cc := cameraClock{doc: v.Time,
		local:  time.Date(2026, 10, 24, 10, 48, 14, 0, time.UTC),
		offset: -time.Duration(n(v.Time, "timeZone"))*time.Second + time.Duration(n(v.Dst, "offset"))*time.Hour}
	utc := time.Date(2026, 10, 24, 15, 48, 14, 0, time.UTC)
	if got := driftText(cc.drift(utc)); got != "in sync" {
		t.Fatalf("drift at the same instant: %s", got)
	}
	if got := driftText(cc.drift(utc.Add(70 * time.Second))); got != "1 min 10 s slow" {
		t.Fatalf("a minute later: %s", got)
	}
	if got := driftText(cc.drift(utc.Add(-2 * time.Hour))); got != "2 h 0 min fast" {
		t.Fatalf("two hours early: %s", got)
	}
	if got := driftText(-2 * time.Second); got != "in sync" {
		t.Fatalf("2 s slow reads %q", got)
	}
}

func TestDailySyncTimeMustBeHHMM(t *testing.T) {
	for _, ok := range []string{"03:00", "23:59", "00:00"} {
		if !hhmm.MatchString(ok) {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"3:00", "24:00", "03:60", "noon"} {
		if hhmm.MatchString(bad) {
			t.Errorf("%s accepted", bad)
		}
	}
}

// back_door's own reply (RLC-810A, v3.1.0.956): no isDst, only the rules,
// and 12:08 at 17:08 UTC on Oct 24 2026 is right (daylight time until the
// last Sunday of October). It must read in sync, not an hour fast.
func TestDaylightSavingFromTheRulesWhenTheCameraDoesNotSay(t *testing.T) {
	tm := map[string]any{"year": 2026.0, "mon": 10.0, "day": 24.0, "hour": 12.0, "min": 8.0, "sec": 29.0, "timeZone": 21600.0}
	dst := map[string]any{"enable": 1.0, "offset": 1.0, "startMon": 3.0, "startWeek": 2.0, "startWeekday": 0.0, "startHour": 2.0,
		"endMon": 10.0, "endWeek": 5.0, "endWeekday": 0.0, "endHour": 2.0}
	std := -6 * time.Hour
	now := time.Date(2026, 10, 24, 17, 8, 29, 0, time.UTC)
	if !dstNow(tm, dst, now, std) {
		t.Fatal("Oct 24 is daylight time by these rules")
	}
	if dstNow(tm, dst, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), std) {
		t.Fatal("Oct 26 is after the last Sunday of October")
	}
	if dstNow(tm, dst, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC), std) {
		t.Fatal("Mar 7 is before the second Sunday of March")
	}
	cc := cameraClock{local: time.Date(2026, 10, 24, 12, 8, 29, 0, time.UTC), offset: std + time.Hour}
	if got := driftText(cc.drift(now)); got != "in sync" {
		t.Fatalf("back_door reads %s", got)
	}
	if got := nthWeekday(2026, time.October, 5, time.Sunday).Day(); got != 25 {
		t.Fatalf("last Sunday of October 2026 = %d", got)
	}
	if got := nthWeekday(2026, time.March, 2, time.Sunday).Day(); got != 8 {
		t.Fatalf("second Sunday of March 2026 = %d", got)
	}
}
