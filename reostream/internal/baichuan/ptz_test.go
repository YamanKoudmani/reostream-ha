package baichuan

import "testing"

// The guard document as a TrackMix PoE returns it (message 332, 2026-10-23).
const trackmixGuard = `<?xml version="1.0" encoding="UTF-8" ?>
<body>
<PtzGuard version="1.1">
<channelId>0</channelId>
<timeout>164</timeout>
<benable>0</benable>
<bvalid>1</bvalid>
<imageName></imageName>
<mode>global</mode>
<xpos>0</xpos>
<ypos>0</ypos>
<height>0</height>
<width>0</width>
</PtzGuard>
</body>
`

func TestPtzControlRefusesUnknownCommands(t *testing.T) {
	c := &Conn{}
	if err := c.PtzControl(t.Context(), "zoomin", 34); err == nil {
		t.Fatal("an uncaptured command was sent")
	}
	if err := c.PtzPreset(t.Context(), "delete", 0, "x"); err == nil {
		t.Fatal("an unknown preset command was sent")
	}
}

func TestParseGuardReadsTheTrackMixDocument(t *testing.T) {
	g, err := parseGuard([]byte(trackmixGuard))
	if err != nil {
		t.Fatal(err)
	}
	if g.Enable || g.Timeout != 164 || !g.Valid {
		t.Fatalf("got %+v, want off, 164 s, a guard position set", g)
	}
}
