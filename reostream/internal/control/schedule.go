package control

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Weekly schedules: recording per detection type (rec task, 81/82), push
// (219/218), siren on detection (audio task, 232/231) and auto tracking
// (436/437). Each is a table of one character per hour, "1" on and "0" off,
// captured from the Reolink app on 2026-10-26. The first three keep one
// table per detection type in a typeScheduleList, where the smart-AI types
// (crossline, intrude, ...) also carry an index; tracking has one timeTable.

// schedItem is one table in a schedule block. Index is -1 for an item
// without one.
type schedItem struct {
	Type  string
	Index int
	Table string
}

// schedSpan is where one item's table text sits in the document.
type schedSpan struct {
	item       schedItem
	start, end int
}

// scanSchedules finds every table in doc, in document order.
func scanSchedules(doc []byte) []schedSpan {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out []schedSpan
	var stack []string
	var cur schedSpan
	inItem, textStart := false, -1
	for {
		off := int(dec.InputOffset())
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			switch t.Name.Local {
			case "item":
				if len(stack) >= 2 && stack[len(stack)-2] == "typeScheduleList" {
					inItem = true
					cur = schedSpan{item: schedItem{Index: -1}, start: -1}
				}
			case "valueTable", "timeTable", "type", "index":
				textStart = int(dec.InputOffset())
			}
		case xml.EndElement:
			name := t.Name.Local
			stack = stack[:len(stack)-1]
			if textStart < 0 {
				if name == "item" && inItem {
					inItem = false
					if cur.start >= 0 {
						out = append(out, cur)
					}
				}
				continue
			}
			text := strings.TrimSpace(string(doc[textStart:off]))
			switch {
			case name == "type" && inItem:
				cur.item.Type = text
			case name == "index" && inItem:
				if n, err := strconv.Atoi(text); err == nil {
					cur.item.Index = n
				}
			case name == "valueTable" && inItem:
				cur.item.Table, cur.start, cur.end = text, textStart, off
			case name == "timeTable" && !inItem:
				out = append(out, schedSpan{item: schedItem{Type: "track", Index: -1, Table: text}, start: textStart, end: off})
			}
			textStart = -1
		}
	}
}

// parseSchedules lists every table a schedule block holds.
func parseSchedules(doc []byte) []schedItem {
	var out []schedItem
	for _, s := range scanSchedules(doc) {
		out = append(out, s.item)
	}
	return out
}

// setSchedule replaces the table of the item named by typ and index, and
// nothing else. The new table must be the same length as the old one and
// only 0s and 1s; an item the camera did not send is refused, never added.
func setSchedule(doc []byte, typ string, index int, table string) ([]byte, error) {
	if strings.Trim(table, "01") != "" {
		return nil, fmt.Errorf("a schedule is only 0s and 1s")
	}
	for _, s := range scanSchedules(doc) {
		if s.item.Type != typ || s.item.Index != index {
			continue
		}
		if len(table) != len(s.item.Table) {
			return nil, fmt.Errorf("this camera's %s schedule has %d slots, not %d", typ, len(s.item.Table), len(table))
		}
		out := make([]byte, 0, len(doc))
		out = append(out, doc[:s.start]...)
		out = append(out, table...)
		return append(out, doc[s.end:]...), nil
	}
	return nil, fmt.Errorf("this camera has no %s schedule", typ)
}

// schedBlocks are the schedule blocks, by settings category.
var schedBlocks = map[string]struct{ Block, Title, Blurb string }{
	"record": {"rec task get", "Recording schedule", "When each kind of detection records to the SD card. Continuous records all the time in the hours marked."},
	"push":   {"push task get", "Push schedule", "When each kind of detection sends a notification."},
	"audio":  {"audio task get", "Siren schedule", "When each kind of detection sounds the siren, while the siren is on."},
	"ptz":    {"get ai track task cfg", "Tracking schedule", "When auto tracking follows what it sees."},
}

// schedSection is one schedule block on a settings page.
type schedSection struct {
	Title, Blurb, Block string
	Items               []schedView
	Err                 string
}

// schedView is one table, labeled.
type schedView struct {
	Type  string
	Index int
	Label string
	Table string
}

// schedLabel names a schedule item the way the Reolink app does.
func schedLabel(it schedItem) string {
	names := map[string]string{
		"MD": "Any motion", "Normal": "Continuous", "people": "Person", "vehicle": "Vehicle",
		"dog_cat": "Animal", "face": "Face", "package": "Package", "visitor": "Visitor",
		"crossline": "Line crossing", "intrude": "Zone intrusion", "loitering": "Loitering",
		"legacy": "Forgotten object", "loss": "Object removal", "track": "Tracking",
	}
	label, ok := names[it.Type]
	if !ok {
		label = it.Type
	}
	if it.Index >= 0 {
		label += " " + strconv.Itoa(it.Index+1)
	}
	return label
}

// readSchedule reads cat's schedule block, if it has one, onto the page.
func (s *Server) readSchedule(ctx context.Context, conn *baichuan.Conn, page *cameraPage, cat string) {
	sb, ok := schedBlocks[cat]
	if !ok {
		return
	}
	pair, ok := pairForName(sb.Block)
	if !ok {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, pair.Get)
	cancel()
	if err != nil || status != 200 {
		// A camera without the feature (no tracking, no siren) simply
		// shows no schedule for it.
		return
	}
	sec := schedSection{Title: sb.Title, Blurb: sb.Blurb, Block: sb.Block}
	items := parseSchedules(doc)
	// The smart-rule tables (crossline 0-2, ...) exist whether or not the
	// rule does; only the ones for a rule the camera has are shown. Table
	// index i belongs to rule index i+1.
	var exists map[string]bool
	for _, it := range items {
		if it.Index >= 0 && exists == nil {
			exists = map[string]bool{}
			for _, set := range s.readRules(ctx, conn) {
				for _, r := range set.Rules {
					exists[set.Kind.Key+":"+strconv.Itoa(r.Index-1)] = true
				}
			}
		}
	}
	for _, it := range items {
		if it.Index >= 0 && !exists[it.Type+":"+strconv.Itoa(it.Index)] {
			continue
		}
		sec.Items = append(sec.Items, schedView{Type: it.Type, Index: it.Index, Label: schedLabel(it), Table: it.Table})
	}
	if len(sec.Items) > 0 {
		page.Schedules = append(page.Schedules, sec)
	}
}

// serveSchedule writes one schedule table: read the block fresh, replace
// that one item's table, write it back through writeBlock.
func (s *Server) serveSchedule(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	block := r.FormValue("block")
	known := false
	for _, sb := range schedBlocks {
		known = known || sb.Block == block
	}
	pair, ok := pairForName(block)
	if !known || !ok {
		http.Error(w, fmt.Sprintf("%q is not a schedule", block), http.StatusBadRequest)
		return
	}
	typ := r.FormValue("type")
	index, err := strconv.Atoi(r.FormValue("index"))
	if err != nil {
		http.Error(w, "bad index", http.StatusBadRequest)
		return
	}
	table := r.FormValue("table")
	back := returnTo(r, "/cameras/"+url.PathEscape(cam.Name))
	subject := fmt.Sprintf("%s: %s schedule", cam.Name, strings.ToLower(schedLabel(schedItem{Type: typ, Index: index})))

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading %s before writing: %v", block, err), http.StatusBadGateway)
		return
	}
	rctx, rcancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, pair.Get)
	rcancel()
	conn.Close()
	if err != nil || status != 200 {
		http.Error(w, fmt.Sprintf("reading %s before writing: %v (status %d)", block, err, status), http.StatusBadGateway)
		return
	}
	body, err := setSchedule(doc, typ, index, table)
	if err != nil {
		s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "refused", Detail: err.Error()}))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	result, err := s.writeBlock(ctx, cam, pair.Set, body, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.setFlash(w, r, flashFor(subject, result))
	http.Redirect(w, r, back, http.StatusSeeOther)
}
