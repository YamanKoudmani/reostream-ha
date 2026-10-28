package control

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const threeCameras = `# header comment

listen = "0.0.0.0:8560"

[[camera]]
name = "one"
address = "192.0.2.50"

# the shed
[[camera]]
name = "two"
address = "192.0.2.51"

[[camera]]
name = "three"
address = "192.0.2.52"
`

func cameraNames(t *testing.T, text string) []string {
	t.Helper()
	var doc struct{ Camera []struct{ Name string } }
	if _, err := toml.Decode(text, &doc); err != nil {
		t.Fatalf("reordered file does not parse: %v\n%s", err, text)
	}
	var out []string
	for _, c := range doc.Camera {
		out = append(out, c.Name)
	}
	return out
}

func TestReorderCameraBlocksMovesWholeBlocks(t *testing.T) {
	got, ok := reorderCameraBlocks(threeCameras, []string{"one", "two", "three"}, []string{"three", "one", "two"})
	if !ok {
		t.Fatal("reorderCameraBlocks could not place a block")
	}
	if names := strings.Join(cameraNames(t, got), ","); names != "three,one,two" {
		t.Errorf("order %s, want three,one,two:\n%s", names, got)
	}
	for _, want := range []string{"# header comment", `listen = "0.0.0.0:8560"`, `address = "192.0.2.52"`, "# the shed"} {
		if !strings.Contains(got, want) {
			t.Errorf("reordering lost %q:\n%s", want, got)
		}
	}
	if len(got) != len(threeCameras) {
		t.Errorf("reordering changed the file's length from %d to %d:\n%s", len(threeCameras), len(got), got)
	}
}

func TestReorderCameraBlocksKeepsUnlistedCamerasAfter(t *testing.T) {
	got, _ := reorderCameraBlocks(threeCameras, []string{"one", "two", "three"}, []string{"two", "nope"})
	if names := strings.Join(cameraNames(t, got), ","); names != "two,one,three" {
		t.Errorf("order %s, want two,one,three:\n%s", names, got)
	}
}

func TestReorderCameraBlocksSameOrderIsUnchanged(t *testing.T) {
	got, _ := reorderCameraBlocks(threeCameras, []string{"one", "two", "three"}, []string{"one", "two", "three"})
	if got != threeCameras {
		t.Errorf("the same order changed the file:\n%s", got)
	}
}
