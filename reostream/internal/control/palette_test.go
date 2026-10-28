package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A camera with no PTZ must not get search results that lead to its PTZ
// page, which does not exist for it (the tracking method field did).
func TestPaletteListsOnlySectionsTheCameraHas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	text := "[[camera]]\nname = \"yard\"\naddress = \"192.0.2.9\"\nstreams = [\"main\"]\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := string(paletteIndex(path))
	if strings.Contains(idx, "/cameras/yard/settings/ptz") {
		t.Fatal("search offers a PTZ page for a camera without PTZ")
	}
	if !strings.Contains(idx, "/cameras/yard/settings/image") {
		t.Fatal("search lost the camera's image settings")
	}
}
