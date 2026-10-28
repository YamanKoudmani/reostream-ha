package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Detection zones. A camera keeps its motion zone in the motion block (md,
// 46/47) and its person, vehicle and animal zones in the AI blocks (342 per
// type, written together with alarm area 345). The Reolink app paints one
// zone and writes it to all of them, which is what this does too (captured
// 2026-10-26).
//
// Both are bitmaps of the picture divided into W x H cells, 1 = detect,
// row-major from the top left, most significant bit first, base64. The motion
// grid sits in the top-left corner of a larger Cols x Rows field (60x33 in
// 96x64 on a TrackMix; 120x67 filling 120x67 on an RLC-810A); the AI areas
// are packed, Cols = W.

// zoneGrid is one bitmap's geometry.
type zoneGrid struct{ W, H, Cols, Rows int }

// decodeZone turns a camera's bitmap into a W*H string of 0s and 1s.
func decodeZone(b64 string, g zoneGrid) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("zone: %w", err)
	}
	if g.W <= 0 || g.H <= 0 || g.Cols < g.W || g.Rows < g.H {
		return "", fmt.Errorf("zone: bad grid %+v", g)
	}
	if len(raw)*8 < (g.H-1)*g.Cols+g.W {
		return "", fmt.Errorf("zone: %d bytes is too short for %dx%d in %d columns", len(raw), g.W, g.H, g.Cols)
	}
	var b strings.Builder
	for r := 0; r < g.H; r++ {
		for c := 0; c < g.W; c++ {
			bit := r*g.Cols + c
			if raw[bit/8]&(0x80>>(bit%8)) != 0 {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
	}
	return b.String(), nil
}

// encodeZone is decodeZone's inverse: the W x H mask in a Cols x Rows field,
// everything outside the mask 0.
func encodeZone(mask string, g zoneGrid) string {
	return encodeZoneOver(mask, g, "")
}

// encodeZoneOver writes the mask into the camera's current bitmap, keeping
// every bit outside the W x H zone as the camera had it: the camera reads
// those back as 0 while the Reolink app writes them as 1, and nothing here
// needs to pick a side.
func encodeZoneOver(mask string, g zoneGrid, current string) string {
	raw := make([]byte, (g.Cols*g.Rows+7)/8)
	if cur, err := base64.StdEncoding.DecodeString(strings.TrimSpace(current)); err == nil {
		copy(raw, cur)
	}
	for r := 0; r < g.H; r++ {
		for c := 0; c < g.W; c++ {
			bit := r*g.Cols + c
			if mask[r*g.W+c] == '1' {
				raw[bit/8] |= 0x80 >> (bit % 8)
			} else {
				raw[bit/8] &^= 0x80 >> (bit % 8)
			}
		}
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// resampleZone scales a w x h mask to nw x nh, nearest cell.
func resampleZone(mask string, w, h, nw, nh int) string {
	if w == nw && h == nh {
		return mask
	}
	var b strings.Builder
	for r := 0; r < nh; r++ {
		for c := 0; c < nw; c++ {
			b.WriteByte(mask[(r*h/nh)*w+c*w/nw])
		}
	}
	return b.String()
}

// alarmAreaDoc is the alarm area write (345), which has no read to echo:
// one area per AI type, all the same zone, in the Reolink app's own shape.
func alarmAreaDoc(channel int, g zoneGrid, mask string) []byte {
	area := encodeZone(mask, zoneGrid{W: g.W, H: g.H, Cols: g.W, Rows: g.H})
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?>` + "\n<body>\n<AlarmArea version=\"1.1\">\n")
	fmt.Fprintf(&b, "<chn>%d</chn>\n", channel)
	for _, name := range []string{"personArea", "vehicleArea", "dogCatArea"} {
		fmt.Fprintf(&b, "<%s>\n<width>%d</width>\n<height>%d</height>\n<area>%s</area>\n</%s>\n", name, g.W, g.H, area, name)
	}
	b.WriteString("</AlarmArea>\n</body>\n")
	return b.Bytes()
}

// mdGrid reads a motion block's geometry.
func mdGrid(doc []byte) (zoneGrid, string, bool) {
	num := func(p string) int {
		v, _ := fieldValue(doc, p)
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	g := zoneGrid{W: num("MD/width"), H: num("MD/height"), Cols: num("MD/scope/columns"), Rows: num("MD/scope/rows")}
	table, ok := fieldValue(doc, "MD/scope/valueTable")
	return g, table, ok && g.W > 0 && g.H > 0 && g.Cols >= g.W && g.Rows >= g.H
}

// aiGrid reads the person block's geometry, when the camera has one.
func aiGrid(doc []byte) (zoneGrid, bool) {
	num := func(p string) int {
		v, _ := fieldValue(doc, p)
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	g := zoneGrid{W: num("AiDetectCfg/width"), H: num("AiDetectCfg/height")}
	g.Cols, g.Rows = g.W, g.H
	return g, g.W > 0 && g.H > 0
}

// zoneView is the detection zone on the motion page.
type zoneView struct {
	W, H  int
	Mask  string
	HasAI bool
}

// readZone reads the motion zone onto the page.
func (s *Server) readZone(ctx context.Context, conn *baichuan.Conn, page *cameraPage) {
	pair, ok := pairForName("md get")
	if !ok {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, pair.Get)
	cancel()
	if err != nil || status != 200 {
		return
	}
	g, table, ok := mdGrid(doc)
	if !ok {
		return
	}
	mask, err := decodeZone(table, g)
	if err != nil {
		return
	}
	z := &zoneView{W: g.W, H: g.H, Mask: mask}
	if ai, ok := pairForName("ai detect cfg get"); ok {
		rctx, cancel := context.WithTimeout(ctx, readTimeout)
		d, st, err := baichuan.ReadConfigWith(rctx, conn, ai.Get, blockRequest("ai detect cfg get#people", conn.Channel()))
		cancel()
		_, has := aiGrid(d)
		z.HasAI = err == nil && st == 200 && has
	}
	page.Zone = z
}

// serveZone writes the detection zone: the motion grid through writeBlock,
// then, on a camera with AI detection, the same zone to every AI type
// through the alarm area, verified by reading the person zone back.
func (s *Server) serveZone(w http.ResponseWriter, r *http.Request) {
	cam, err := s.byName(r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	mask := r.FormValue("mask")
	back := returnTo(r, "/cameras/"+url.PathEscape(cam.Name))
	subject := cam.Name + ": detection zone"
	refuse := func(why string) {
		s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "refused", Detail: why}))
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
	if mask == "" || strings.Trim(mask, "01") != "" {
		refuse("a zone is only 0s and 1s")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		http.Error(w, fmt.Sprintf("reading the motion zone before writing: %v", err), http.StatusBadGateway)
		return
	}
	md, _ := pairForName("md get")
	rctx, rcancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, md.Get)
	rcancel()
	var aiDoc []byte
	var aiStatus int16
	ai, haveAI := pairForName("ai detect cfg get")
	if haveAI && err == nil {
		rctx, rcancel := context.WithTimeout(ctx, readTimeout)
		aiDoc, aiStatus, _ = baichuan.ReadConfigWith(rctx, conn, ai.Get, blockRequest("ai detect cfg get#people", cam.Channel))
		rcancel()
	}
	conn.Close()
	if err != nil || status != 200 {
		http.Error(w, fmt.Sprintf("reading the motion zone before writing: %v (status %d)", err, status), http.StatusBadGateway)
		return
	}
	g, current, ok := mdGrid(doc)
	if !ok {
		refuse("this camera's motion block has no zone grid")
		return
	}
	if len(mask) != g.W*g.H {
		refuse(fmt.Sprintf("this camera's zone is %dx%d, not %d cells", g.W, g.H, len(mask)))
		return
	}
	body, err := setField(doc, "MD/scope/valueTable", encodeZoneOver(mask, g, current))
	if err != nil {
		refuse(err.Error())
		return
	}
	result, err := s.writeBlock(ctx, cam, md.Set, body, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if result.Outcome != "refused" && aiStatus == 200 {
		if ag, ok := aiGrid(aiDoc); ok {
			aiMask := resampleZone(mask, g.W, g.H, ag.W, ag.H)
			if why := s.writeAlarmArea(ctx, cam, ag, aiMask); why != "" {
				result.Outcome = "refused"
				result.Detail = "the motion zone was saved, but the person, vehicle and animal zones were not: " + why
			}
		}
	}
	s.setFlash(w, r, flashFor(subject, result))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// writeAlarmArea sends the alarm area (345) and checks the person zone
// reads back as written; "" when it did.
func (s *Server) writeAlarmArea(ctx context.Context, cam Camera, g zoneGrid, mask string) string {
	conn, err := s.dial(ctx, cam)
	if err != nil {
		return err.Error()
	}
	defer conn.Close()
	wctx, cancel := context.WithTimeout(ctx, readTimeout)
	status, err := baichuan.WriteConfig(wctx, conn, 345, alarmAreaDoc(cam.Channel, g, mask))
	cancel()
	if err != nil {
		return err.Error()
	}
	if status != 200 {
		return baichuan.ExplainStatus(status)
	}
	ai, _ := pairForName("ai detect cfg get")
	rctx, rcancel := context.WithTimeout(ctx, readTimeout)
	doc, st, err := baichuan.ReadConfigWith(rctx, conn, ai.Get, blockRequest("ai detect cfg get#people", cam.Channel))
	rcancel()
	if err != nil || st != 200 {
		return "could not read the person zone back"
	}
	area, _ := fieldValue(doc, "AiDetectCfg/area")
	got, err := decodeZone(area, zoneGrid{W: g.W, H: g.H, Cols: g.W, Rows: g.H})
	if err != nil || got != mask {
		return "the camera accepted it but the person zone reads back different"
	}
	return ""
}
