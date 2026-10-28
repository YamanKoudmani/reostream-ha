package control

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// Privacy mask: up to maxNum black boxes the camera draws into every stream
// (shelter, 52/53). Each corner and size is a fraction of the picture packed
// as numerator<<16 | denominator: the Reolink app wrote 72/384 for a box
// starting near 0.19 of the width, and 50/100 written from here blacked out
// exactly half of a TrackMix's picture (2026-10-26). The read lists boxes as
// shelterList; the write is the app's own ShelterList shape, composed
// because the read and the write differ.

// maskRect is one box as fractions of the picture.
type maskRect struct{ X, Y, W, H float64 }

const maskDen = 1000

func maskFrac(v int64) float64 {
	den := v & 0xffff
	if den == 0 {
		return 0
	}
	return float64(v>>16) / float64(den)
}

func maskValue(f float64) int64 {
	f = math.Max(0, math.Min(1, f))
	return int64(math.Round(f*maskDen))<<16 | maskDen
}

// parseMasks reads the enabled boxes and the most the camera allows.
func parseMasks(doc []byte) ([]maskRect, int) {
	maxNum := 4
	if v, ok := fieldValue(doc, "Shelter/maxNum"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			maxNum = n
		}
	}
	var out []maskRect
	// The list is a run of <Shelter> elements inside shelterList; each is
	// cut out and read on its own so its fields cannot be confused with a
	// neighbor's.
	s := string(doc)
	start := strings.Index(s, "<shelterList>")
	end := strings.Index(s, "</shelterList>")
	if start < 0 || end < start {
		return nil, maxNum
	}
	list := s[start:end]
	for _, part := range strings.Split(list, "<Shelter>")[1:] {
		item := []byte("<Shelter>" + strings.SplitN(part, "</Shelter>", 2)[0] + "</Shelter>")
		get := func(p string) int64 {
			v, _ := fieldValue(item, "Shelter/"+p)
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
		if get("enable") != 1 {
			continue
		}
		out = append(out, maskRect{maskFrac(get("topLeftX")), maskFrac(get("topLeftY")), maskFrac(get("width")), maskFrac(get("height"))})
	}
	return out, maxNum
}

// maskDoc is the write: every slot listed, the used ones with their box.
func maskDoc(channel, slots int, rects []maskRect) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" ?>` + "\n<body>\n<Shelter version=\"1.1\">\n<logicChannel>-1</logicChannel>\n")
	enable := 0
	if len(rects) > 0 {
		enable = 1
	}
	fmt.Fprintf(&b, "<channelId>%d</channelId>\n<enable>%d</enable>\n<ShelterList>\n", channel, enable)
	for i := 0; i < slots; i++ {
		if i < len(rects) {
			r := rects[i]
			fmt.Fprintf(&b, "<Shelter>\n<id>%d</id>\n<enable>1</enable>\n<topLeftX>%d</topLeftX>\n<topLeftY>%d</topLeftY>\n<width>%d</width>\n<height>%d</height>\n</Shelter>\n",
				i, maskValue(r.X), maskValue(r.Y), maskValue(r.W), maskValue(r.H))
		} else {
			fmt.Fprintf(&b, "<Shelter>\n<id>%d</id>\n<enable>0</enable>\n</Shelter>\n", i)
		}
	}
	b.WriteString("</ShelterList>\n<trackEnable>0</trackEnable>\n<trackShelterList />\n</Shelter>\n</body>\n")
	return b.Bytes()
}

// maskView is the privacy mask editor's data.
type maskView struct {
	Max   int
	Rects string // "x,y,w,h;..." fractions, for the page script
}

func (s *Server) readMask(ctx context.Context, conn *baichuan.Conn, page *cameraPage) {
	pair, ok := pairForName("shelter get")
	if !ok {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, pair.Get)
	cancel()
	if err != nil || status != 200 {
		return
	}
	rects, max := parseMasks(doc)
	page.Mask = &maskView{Max: max, Rects: formatRects(rects)}
}

func formatRects(rs []maskRect) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%.4f,%.4f,%.4f,%.4f", r.X, r.Y, r.W, r.H))
	}
	return strings.Join(parts, ";")
}

func parseRects(s string) ([]maskRect, error) {
	var out []maskRect
	for _, part := range strings.Split(strings.TrimSpace(s), ";") {
		if part == "" {
			continue
		}
		f := strings.Split(part, ",")
		if len(f) != 4 {
			return nil, fmt.Errorf("a box is x,y,width,height")
		}
		var v [4]float64
		for i := range f {
			n, err := strconv.ParseFloat(strings.TrimSpace(f[i]), 64)
			if err != nil || n < 0 || n > 1 {
				return nil, fmt.Errorf("box values are fractions of the picture, 0 to 1")
			}
			v[i] = n
		}
		out = append(out, maskRect{v[0], v[1], v[2], v[3]})
	}
	return out, nil
}

// serveMask writes the privacy mask through writeBlock.
func (s *Server) serveMask(w http.ResponseWriter, r *http.Request) {
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
	subject := cam.Name + ": privacy mask"
	rects, err := parseRects(r.FormValue("rects"))
	if err != nil {
		s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "refused", Detail: err.Error()}))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := s.dial(ctx, cam)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	pair, _ := pairForName("shelter get")
	rctx, rcancel := context.WithTimeout(ctx, readTimeout)
	doc, status, err := baichuan.ReadConfig(rctx, conn, pair.Get)
	rcancel()
	conn.Close()
	if err != nil || status != 200 {
		http.Error(w, fmt.Sprintf("reading the privacy mask before writing: %v (status %d)", err, status), http.StatusBadGateway)
		return
	}
	_, max := parseMasks(doc)
	if len(rects) > max {
		s.setFlash(w, r, flashFor(subject, WriteResult{Outcome: "refused", Detail: fmt.Sprintf("this camera takes at most %d boxes", max)}))
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	result, err := s.writeBlock(ctx, cam, pair.Set, maskDoc(cam.Channel, max, rects), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.setFlash(w, r, flashFor(subject, checkMasks(result, rects)))
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// checkMasks settles an accepted mask write by the boxes themselves. The
// byte-for-byte check in outcomeFor can never pass here, because the camera
// reads masks back as shelterList while the write is ShelterList, so every
// save would otherwise be reported as unconfirmed even when it took.
func checkMasks(result WriteResult, want []maskRect) WriteResult {
	if result.Outcome != "accepted" || len(result.After) == 0 {
		return result
	}
	got, _ := parseMasks(result.After)
	if masksEqual(got, want) {
		result.Outcome = "confirmed"
		result.Detail = fmt.Sprintf("confirmed: read back on a fresh connection, the camera holds %d box(es) as drawn.", len(want))
		return result
	}
	result.Outcome = "refused"
	result.Detail = fmt.Sprintf("the camera answered OK but did not keep it: it holds %d box(es), %d were drawn.", len(got), len(want))
	return result
}

// masksEqual compares boxes within the rounding the camera's fractions
// allow; the order the camera lists them in does not matter.
func masksEqual(a, b []maskRect) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for j, y := range b {
			if !used[j] && near(x.X, y.X) && near(x.Y, y.Y) && near(x.W, y.W) && near(x.H, y.H) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }
