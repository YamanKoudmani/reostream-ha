package control

import (
	"strings"
	"testing"
)

// A privacy mask corner or size is a fraction of the picture packed as
// numerator<<16 | denominator. The Reolink app wrote 4718976 (72/384) for a
// box starting near 0.19 of the width; reostream wrote 3276900 (50/100) and
// the TrackMix blacked out exactly half the picture (2026-10-26).
func TestMaskFractionsDecodeAndEncode(t *testing.T) {
	if got := maskFrac(4718976); got < 0.187 || got > 0.188 {
		t.Errorf("maskFrac(4718976) = %v, want 72/384", got)
	}
	if got := maskFrac(3276900); got != 0.5 {
		t.Errorf("maskFrac(3276900) = %v, want 0.5", got)
	}
	if got := maskValue(0.5); maskFrac(got) != 0.5 {
		t.Errorf("maskValue(0.5) = %d does not decode to 0.5", got)
	}
}

func TestParseMasksReadsTheCamerasList(t *testing.T) {
	doc := []byte(`<body><Shelter version="1.1"><channelId>0</channelId><enable>1</enable><maxNum>4</maxNum><shelterList><Shelter><id>0</id><enable>1</enable><layer>0</layer><color>0</color><topLeftX>100</topLeftX><topLeftY>100</topLeftY><width>3276900</width><height>3276900</height></Shelter><Shelter><id>1</id><enable>0</enable><topLeftX>0</topLeftX><topLeftY>0</topLeftY><width>0</width><height>0</height></Shelter></shelterList><trackEnable>0</trackEnable></Shelter></body>`)
	rects, max := parseMasks(doc)
	if max != 4 || len(rects) != 1 || rects[0] != (maskRect{0, 0, 0.5, 0.5}) {
		t.Fatalf("parseMasks = %+v, %d", rects, max)
	}
}

func TestMaskDocumentHasTheAppsShape(t *testing.T) {
	doc := string(maskDoc(0, 4, []maskRect{{0.1, 0.2, 0.3, 0.4}}))
	for _, want := range []string{"<logicChannel>-1</logicChannel>", "<enable>1</enable>", "<ShelterList>", "<id>0</id>", "<topLeftX>6554600</topLeftX>", "<id>3</id>", "<trackShelterList />"} {
		if !strings.Contains(doc, want) {
			t.Errorf("mask document lacks %q:\n%s", want, doc)
		}
	}
	if strings.Count(doc, "<Shelter>") != 4 {
		t.Errorf("want 4 slots, one used:\n%s", doc)
	}
	off := string(maskDoc(0, 4, nil))
	if !strings.Contains(off, "<Shelter version=\"1.1\">\n<logicChannel>-1</logicChannel>\n<channelId>0</channelId>\n<enable>0</enable>") {
		t.Errorf("no boxes should turn the mask off:\n%s", off)
	}
}

// The camera reads masks back as shelterList and is written as ShelterList,
// so a mask save has to be judged by the boxes, not by the bytes.
func TestMaskSaveIsConfirmedByTheBoxesReadBack(t *testing.T) {
	drawn := []maskRect{{0.17, 0.385, 0.398, 0.378}}
	after := []byte(`<body><Shelter version="1.1"><channelId>0</channelId><enable>1</enable><maxNum>4</maxNum><shelterList>` +
		`<Shelter><id>0</id><enable>1</enable><topLeftX>11142120</topLeftX><topLeftY>25232360</topLeftY><width>26084328</width><height>24773608</height></Shelter>` +
		`<Shelter><id>1</id><enable>0</enable></Shelter></shelterList></Shelter></body>`)
	got := checkMasks(WriteResult{Outcome: "accepted", After: after}, drawn)
	if got.Outcome != "confirmed" {
		t.Errorf("held box reported %q: %s", got.Outcome, got.Detail)
	}
	none := checkMasks(WriteResult{Outcome: "accepted", After: after}, nil)
	if none.Outcome != "refused" {
		t.Errorf("box still held after removing every box reported %q", none.Outcome)
	}
}
