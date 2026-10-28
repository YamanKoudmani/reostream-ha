package control

import (
	"io/fs"
	"regexp"
	"sort"
	"testing"
)

// TestOnlyTheListedAssetsAreEmbedded guards the directory that is served
// WITHOUT authentication.
//
// assets/ used to be embedded with a bare `//go:embed assets`, which made
// publishing a file the default for anything that ever landed in that
// directory. That is not theoretical: a creds.toml dropped there was served
// to an unauthenticated request with the whole suite still green, because
// nothing anywhere asserted what the directory contains.
//
// Listing the files in the embed directive fixes the default, and this test
// is what stops the wildcard coming back: adding a file to the directory
// alone changes nothing, and adding one to the directive fails here until
// somebody says out loud that it is meant to be world readable.
func TestOnlyTheListedAssetsAreEmbedded(t *testing.T) {
	want := []string{
		"assets/LICENSE-mpegts.txt",
		// The seam aligner. World readable for the same reason as the
		// dewarp renderer: sliders, a canvas and one POST, holding no
		// camera name, address, credential or config.
		"assets/align.js",
		"assets/dashboard.js",
		// The encoder form's own narrowing. World readable on the same
		// terms as the rest: it holds the camera's option table only as
		// the page it runs on already shows it, and no credential.
		"assets/encoding.js",
		// The dewarp renderer and its page wiring. Both are meant to be
		// world readable: they are the same bytes for every install and
		// hold no camera name, address, credential or config -- the
		// projection is arithmetic on a picture the browser already has.
		"assets/fisheye.js",
		"assets/fisheyeview.js",
		"assets/live.js",
		"assets/README.md",
		"assets/mpegts.js",
		"assets/mpegts.js.LICENSE.txt",
		"assets/rec.js",
		// The weekly schedule grid; no camera data in it, the tables
		// come with the authenticated page.
		"assets/schedule.js",
		// The detection zone painter; the zone itself comes with the page.
		"assets/zone.js",
		// The privacy mask box drawer; the boxes come with the page.
		"assets/mask.js",
		// The smart rule point editor; the rules come with the page.
		"assets/rules.js",
		// The siren sound uploader: decodes a file in the browser and posts
		// the samples to the authenticated sound endpoint.
		"assets/sound.js",
		"assets/settings.js",
		"assets/snapshot.js",
		"assets/style.css",
		// The camera page's hold-to-talk wiring. World readable on the same
		// terms: microphone capture and a socket to this page's own host,
		// with no camera name, address or credential in it.
		"assets/talk.js",
	}

	var got []string
	err := fs.WalkDir(assetFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded assets: %v", err)
	}
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("embedded assets are %v, want %v -- a file served with no\n"+
			"authentication was added or removed; if that is deliberate, say so here too", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("embedded assets are %v, want %v", got, want)
		}
	}
}

// TestEveryReferencedAssetIsEmbedded catches the other half of the explicit
// list: a template that loads /assets/x.js while x.js was never added to the
// directive gets a 404 in the browser and a control that silently does
// nothing, with every other test green. The talk button shipped that way.
func TestEveryReferencedAssetIsEmbedded(t *testing.T) {
	re := regexp.MustCompile(`/assets/([A-Za-z0-9._-]+)`)
	err := fs.WalkDir(templateFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(templateFS, path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if _, err := fs.Stat(assetFS, "assets/"+m[1]); err != nil {
				t.Errorf("%s loads /assets/%s, which is not embedded", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
