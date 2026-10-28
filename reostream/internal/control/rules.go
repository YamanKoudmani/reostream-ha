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

// Smart rules: line crossing, zone intrusion, loitering, forgotten object
// and object removal, on cameras that have them (a Duo, not an RLC-810A,
// which answers 405). Each is a list of up to maxNum rules with a polygon
// (or, for line crossing, a line) in fractions of the picture. Add and
// delete were captured from the Reolink app; modify was proved on a Duo by
// renaming a rule in place and reading it back (2026-10-26).

// ruleKind is one kind of smart rule and how its documents are named.
type ruleKind struct {
	Key, Title, Root, Item string
	Get, Set               uint32
	Line                   bool   // two points and a direction
	StayTag                string // "stayTime", "timeThresh" or ""
	HasAIType              bool
	Blurb                  string
}

func ruleKinds() []ruleKind {
	return []ruleKind{
		{"crossline", "Line crossing", "CrosslineDetect", "crosslineDetectItem", 527, 528, true, "", true, "Alert when something crosses the line."},
		{"intrude", "Zone intrusion", "IntrusionDetect", "intrusionDetectItem", 529, 530, false, "stayTime", true, "Alert when something enters the zone and stays the set time."},
		{"loitering", "Loitering", "LoiteringDetect", "loiteringDetectItem", 531, 532, false, "stayTime", true, "Alert when something lingers in the zone for the set time."},
		{"legacy", "Forgotten object", "LegacyDetect", "legacyDetectItem", 549, 550, false, "timeThresh", false, "Alert when an object is left in the zone for the set time."},
		{"loss", "Object removal", "LossDetect", "lossDetectItem", 551, 552, false, "timeThresh", false, "Alert when an object disappears from the zone for the set time."},
	}
}

func ruleKindFor(key string) (ruleKind, bool) {
	for _, k := range ruleKinds() {
		if k.Key == key {
			return k, true
		}
	}
	return ruleKind{}, false
}

// rule is one rule; Points is "x,y;x,y;..." in fractions of the picture.
type rule struct {
	Index       int
	Name        string
	AIType      string
	Sensitivity int
	Stay        int
	Direction   string
	Points      string
}

// ruleSet is one kind's rules on one camera.
type ruleSet struct {
	Kind                 ruleKind
	Max                  int
	MinPoints, MaxPoints int
	Rules                []rule
}

// parseRules reads one kind's list.
func parseRules(doc []byte, k ruleKind) ruleSet {
	set := ruleSet{Kind: k, Max: 3, MinPoints: 4, MaxPoints: 8}
	if k.Line {
		set.MinPoints = 2
	}
	num := func(d []byte, p string) (int, bool) {
		v, ok := fieldValue(d, p)
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, ok && err == nil
	}
	if n, ok := num(doc, k.Root+"/maxNum"); ok {
		set.Max = n
	}
	if n, ok := num(doc, k.Root+"/pointMinNum"); ok {
		set.MinPoints = n
	}
	// The camera spells it pointMaxNUm.
	if n, ok := num(doc, k.Root+"/pointMaxNUm"); ok {
		set.MaxPoints = n
	}
	s := string(doc)
	open, closeTag := "<"+k.Item+">", "</"+k.Item+">"
	for _, part := range strings.Split(s, open)[1:] {
		item := []byte(open + strings.SplitN(part, closeTag, 2)[0] + closeTag)
		get := func(p string) string { v, _ := fieldValue(item, k.Item+"/"+p); return strings.TrimSpace(v) }
		r := rule{Name: get("name"), AIType: get("aiType"), Direction: get("direction")}
		r.Index, _ = strconv.Atoi(get("index"))
		r.Sensitivity, _ = strconv.Atoi(get("sesensitivity"))
		if k.StayTag != "" {
			r.Stay, _ = strconv.Atoi(get(k.StayTag))
		}
		var pts []string
		for _, p := range strings.Split(string(item), "<point>")[1:] {
			pt := []byte("<point>" + strings.SplitN(p, "</point>", 2)[0] + "</point>")
			x, _ := fieldValue(pt, "point/x")
			y, _ := fieldValue(pt, "point/y")
			fx, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
			fy, _ := strconv.ParseFloat(strings.TrimSpace(y), 64)
			pts = append(pts, strconv.FormatFloat(fx, 'f', -1, 64)+","+strconv.FormatFloat(fy, 'f', -1, 64))
		}
		r.Points = strings.Join(pts, ";")
		set.Rules = append(set.Rules, r)
	}
	return set
}

func xmlText(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ruleDoc is the write for one rule, op add, modify or delete.
func ruleDoc(k ruleKind, op string, channel int, r rule) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?>` + "\n<body>\n")
	fmt.Fprintf(&b, "<%s version=\"1.1\">\n<channelId>%d</channelId>\n<op>%s</op>\n<%s>\n<index>%d</index>\n<name>%s</name>\n", k.Root, channel, op, k.Item, r.Index, xmlText(r.Name))
	if k.HasAIType {
		fmt.Fprintf(&b, "<aiType>%s</aiType>\n", xmlText(r.AIType))
	}
	if k.StayTag == "timeThresh" {
		fmt.Fprintf(&b, "<timeThresh>%d</timeThresh>\n", r.Stay)
	}
	fmt.Fprintf(&b, "<sesensitivity>%d</sesensitivity>\n", r.Sensitivity)
	if k.StayTag == "stayTime" {
		fmt.Fprintf(&b, "<stayTime>%d</stayTime>\n", r.Stay)
	}
	if k.StayTag == "timeThresh" {
		b.WriteString("<minTargetHeight>0.000000e+00</minTargetHeight>\n<maxTargetHeight>0.000000e+00</maxTargetHeight>\n<minTargetWidth>0.000000e+00</minTargetWidth>\n<maxTargetWidth>0.000000e+00</maxTargetWidth>\n")
	}
	b.WriteString("<pointList>\n")
	for _, p := range strings.Split(r.Points, ";") {
		xy := strings.Split(p, ",")
		if len(xy) != 2 {
			continue
		}
		x, _ := strconv.ParseFloat(xy[0], 64)
		y, _ := strconv.ParseFloat(xy[1], 64)
		fmt.Fprintf(&b, "<point>\n<x>%e</x>\n<y>%e</y>\n</point>\n", x, y)
	}
	b.WriteString("</pointList>\n")
	if k.Line {
		dir := r.Direction
		if dir == "" {
			dir = "both"
		}
		fmt.Fprintf(&b, "<direction>%s</direction>\n", xmlText(dir))
	}
	fmt.Fprintf(&b, "</%s>\n</%s>\n</body>\n", k.Item, k.Root)
	return b.Bytes()
}

// readRules reads every kind this camera answers.
func (s *Server) readRules(ctx context.Context, conn *baichuan.Conn) []ruleSet {
	var out []ruleSet
	for _, k := range ruleKinds() {
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		doc, status, err := baichuan.ReadConfig(rctx, conn, k.Get)
		cancel()
		if err != nil || status != 200 {
			continue
		}
		out = append(out, parseRules(doc, k))
	}
	return out
}

// serveRule adds, changes or deletes one rule, then reads the list back to
// confirm it.
func (s *Server) serveRule(w http.ResponseWriter, r *http.Request) {
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
	k, ok := ruleKindFor(r.FormValue("kind"))
	op := r.FormValue("op")
	if !ok || (op != "add" && op != "modify" && op != "delete") {
		http.Error(w, "unknown rule or operation", http.StatusBadRequest)
		return
	}
	atoi := func(name string) int { n, _ := strconv.Atoi(r.FormValue(name)); return n }
	ru := rule{Index: atoi("index"), Name: strings.TrimSpace(r.FormValue("rname")), AIType: strings.Join(r.Form["aitype"], ","),
		Sensitivity: atoi("sensitivity"), Stay: atoi("stay"), Direction: r.FormValue("direction"), Points: r.FormValue("points")}
	subject := fmt.Sprintf("%s: %s %q", cam.Name, strings.ToLower(k.Title), ru.Name)
	refuse := func(why string) {
		s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "refused", Detail: why}))
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
	rctx, rcancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, k.Get)
	rcancel()
	if err != nil || status != 200 {
		refuse("this camera does not have " + strings.ToLower(k.Title))
		return
	}
	set := parseRules(doc, k)
	if op != "delete" {
		npts := len(strings.Split(ru.Points, ";"))
		if ru.Points == "" || npts < set.MinPoints || npts > set.MaxPoints {
			refuse(fmt.Sprintf("a %s needs %d to %d points", strings.ToLower(k.Title), set.MinPoints, set.MaxPoints))
			return
		}
		if ru.Name == "" {
			refuse("give the rule a name")
			return
		}
		if k.HasAIType && ru.AIType == "" {
			refuse("pick at least one of person, vehicle or animal")
			return
		}
	}
	if op == "add" {
		used := map[int]bool{}
		for _, x := range set.Rules {
			used[x.Index] = true
		}
		if len(set.Rules) >= set.Max {
			refuse(fmt.Sprintf("this camera takes at most %d", set.Max))
			return
		}
		for i := 1; ; i++ {
			if !used[i] {
				ru.Index = i
				break
			}
		}
	}
	wctx, wcancel := context.WithTimeout(ctx, readTimeout)
	st, err := baichuan.WriteConfig(wctx, conn, k.Set, ruleDoc(k, op, cam.Channel, ru))
	wcancel()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if st != 200 {
		refuse(baichuan.ExplainStatus(st))
		return
	}
	rctx, rcancel = context.WithTimeout(ctx, readTimeout)
	after, _, err := baichuan.ReadConfig(rctx, conn, k.Get)
	rcancel()
	found := false
	for _, x := range parseRules(after, k).Rules {
		found = found || x.Index == ru.Index
	}
	if err != nil || found == (op == "delete") {
		refuse("the camera answered OK but the rule list reads back unchanged")
		return
	}
	s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "confirmed", Detail: op + " confirmed by reading the list back"}))
	http.Redirect(w, r, back, http.StatusSeeOther)
}
