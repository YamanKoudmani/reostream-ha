package control

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpsertFTPSectionKeepsTheRestOfTheFile(t *testing.T) {
	text := "# fleet\nlisten = \"0.0.0.0:8560\"\n\n[control]\nlisten = \"0.0.0.0:8562\"\n\n[[camera]]\nname = \"argus\"\n"
	sec := "[ftp]\nlisten = \":2121\"\npassword = \"x\"\n"
	got := upsertFTPSection(text, sec)
	if !strings.Contains(got, "# fleet") || !strings.Contains(got, "[control]") || strings.Index(got, "[ftp]") > strings.Index(got, "[[camera]]") {
		t.Fatalf("added section in the wrong place or lost text:\n%s", got)
	}
	again := upsertFTPSection(got, "[ftp]\nlisten = \":2200\"\npassword = \"y\"\n")
	if strings.Count(again, "[ftp]") != 1 || !strings.Contains(again, `":2200"`) || strings.Contains(again, `":2121"`) {
		t.Fatalf("replacing the section went wrong:\n%s", again)
	}
	if !strings.Contains(again, "[[camera]]\nname = \"argus\"") {
		t.Fatalf("camera lost:\n%s", again)
	}
}

// moov/mvhd with timescale 1000 and duration 8640: 8.64 s.
func tinyMP4(t *testing.T, path string) {
	mvhd := make([]byte, 8+100)
	binary.BigEndian.PutUint32(mvhd[0:], uint32(len(mvhd)))
	copy(mvhd[4:], "mvhd")
	binary.BigEndian.PutUint32(mvhd[20:], 1000)
	binary.BigEndian.PutUint32(mvhd[24:], 8640)
	moov := append([]byte{0, 0, 0, 0, 'm', 'o', 'o', 'v'}, mvhd...)
	binary.BigEndian.PutUint32(moov[0:], uint32(len(moov)))
	ftyp := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0}
	if err := os.WriteFile(path, append(ftyp, moov...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSavedClipsReadTheCamerasNamesAndLengths(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "argus", "2026", "10", "23")
	os.MkdirAll(day, 0o755)
	tinyMP4(t, filepath.Join(day, "Battery Cam_00_20261023182240.mp4"))
	os.WriteFile(filepath.Join(day, "Battery Cam_00_20261023182241.jpg"), []byte("jpg"), 0o644)
	clips := savedClips(dir, "argus")
	if len(clips) != 2 {
		t.Fatalf("%d clips", len(clips))
	}
	v := clips[0]
	if v.Photo || v.Rel != "2026/10/23/Battery Cam_00_20261023182240.mp4" {
		t.Fatalf("first %+v", v)
	}
	if v.Start.Hour() != 18 || v.Start.Minute() != 22 || v.Start.Second() != 40 {
		t.Fatalf("time from name: %v", v.Start)
	}
	if d := v.End.Sub(v.Start); d < 8600*time.Millisecond || d > 8700*time.Millisecond {
		t.Fatalf("length %v, want 8.64s", d)
	}
	if !clips[1].Photo {
		t.Fatal("the jpg is not marked as a photo")
	}
}

func TestPruneClipsDeletesOnlyOldOnes(t *testing.T) {
	dir := t.TempDir()
	oldDay := filepath.Join(dir, "argus", "2026", "09", "01")
	newDay := filepath.Join(dir, "argus", "2026", "10", "23")
	os.MkdirAll(oldDay, 0o755)
	os.MkdirAll(newDay, 0o755)
	old := filepath.Join(oldDay, "a.mp4")
	fresh := filepath.Join(newDay, "b.mp4")
	os.WriteFile(old, []byte("x"), 0o644)
	os.WriteFile(fresh, []byte("x"), 0o644)
	past := time.Now().AddDate(0, 0, -30)
	os.Chtimes(old, past, past)
	pruneClips(dir, time.Now().AddDate(0, 0, -14))
	if _, err := os.Stat(old); err == nil {
		t.Fatal("an old clip was kept")
	}
	if _, err := os.Stat(oldDay); err == nil {
		t.Fatal("the emptied day folder was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a recent clip was deleted")
	}
}

// The upload address is worked out per camera, from the route to it, so
// nobody has to type it; a typed one wins.
func TestUploadHostComesFromTheRouteToTheCamera(t *testing.T) {
	cam := Camera{Name: "porch", Address: "127.0.0.1"}
	if got := uploadHost(cam, "", ""); got != "" {
		t.Fatalf("loopback route gave %q, want nothing", got)
	}
	if got := uploadHost(cam, "10.0.0.5", ""); got != "10.0.0.5" {
		t.Fatalf("typed address ignored: %q", got)
	}
	if got := uploadHost(cam, "", "192.168.1.20"); got != "192.168.1.20" {
		t.Fatalf("page address not used as the fallback: %q", got)
	}
	if ip := routeAddr("192.0.2.10"); ip != nil && uploadHost(Camera{Address: "192.0.2.10"}, "", "") != ip.String() && !dockerBridge.Contains(netip.AddrFrom4([4]byte(ip))) {
		t.Fatal("route address not used")
	}
}

// Turning auto-save on must not also leave round-the-clock upload on: that
// would send every hour of video, not just motion.
func TestAutoSaveTurnsOffRoundTheClockUpload(t *testing.T) {
	doc := []byte(`<FtpTask><enable>1</enable><typeScheduleList>` +
		`<item><type>MD</type><valueTable>1111</valueTable></item>` +
		`<item><type>Normal</type><valueTable>1101</valueTable></item>` +
		`</typeScheduleList></FtpTask>`)
	got := string(noRoundTheClock(doc))
	if !strings.Contains(got, "<type>Normal</type><valueTable>0000</valueTable>") || !strings.Contains(got, "<type>MD</type><valueTable>1111</valueTable>") {
		t.Fatalf("got %s", got)
	}
}
