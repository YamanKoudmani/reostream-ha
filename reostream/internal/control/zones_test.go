package control

import (
	"strings"
	"testing"
)

// The TrackMix's motion grid and person area as the Reolink app wrote them
// on 2026-10-26, after a stroke was painted out near the top left. The
// motion grid is 60x33 inside a 96x64 field; the person area is the same
// 60x33, packed.
func zoneFixture(t *testing.T) (md, area string) {
	lines := strings.Split(strings.TrimSpace(string(readTestdata(t, "testdata/zone-trackmix.txt"))), "\n")
	return lines[0], lines[1]
}

func TestZoneBitsRoundTripTheAppsOwnBytes(t *testing.T) {
	md, area := zoneFixture(t)
	mdMask, err := decodeZone(md, zoneGrid{W: 60, H: 33, Cols: 96, Rows: 64})
	if err != nil {
		t.Fatal(err)
	}
	aiMask, err := decodeZone(area, zoneGrid{W: 60, H: 33, Cols: 60, Rows: 33})
	if err != nil {
		t.Fatal(err)
	}
	if mdMask != aiMask {
		t.Fatal("the motion grid and the person area hold different zones; the app wrote the same one to both")
	}
	if len(mdMask) != 60*33 || mdMask[0] != '1' || mdMask[5*60+9] != '0' {
		t.Fatalf("mask is not the painted stroke (len %d)", len(mdMask))
	}
	if got := encodeZoneOver(mdMask, zoneGrid{W: 60, H: 33, Cols: 96, Rows: 64}, md); got != md {
		t.Errorf("motion grid re-encoded differently:\n got %s\nwant %s", got, md)
	}
	if got := encodeZone(aiMask, zoneGrid{W: 60, H: 33, Cols: 60, Rows: 33}); got != area {
		t.Errorf("person area re-encoded differently:\n got %s\nwant %s", got, area)
	}
}

// A grid of another size (the pano's AI area against a different motion
// grid) gets the zone scaled, not truncated.
func TestResampleZoneKeepsTheShape(t *testing.T) {
	src := strings.Repeat("1100", 4*4/4) // 4x4: left half on
	out := resampleZone(src, 4, 4, 8, 2)
	if out != "1111000011110000" {
		t.Errorf("resample = %s", out)
	}
}

func TestAlarmAreaDocumentHasTheAppsShape(t *testing.T) {
	doc := string(alarmAreaDoc(0, zoneGrid{W: 2, H: 1, Cols: 2, Rows: 1}, "10"))
	for _, want := range []string{"<AlarmArea version=\"1.1\">", "<chn>0</chn>", "<personArea>", "<vehicleArea>", "<dogCatArea>", "<width>2</width>", "<height>1</height>", "<area>gA==</area>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("AlarmArea document lacks %q:\n%s", want, doc)
		}
	}
}
