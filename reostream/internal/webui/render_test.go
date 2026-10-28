package webui

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// Every page this renders is a reading of a camera's current settings, so a
// stored copy is a wrong one as soon as anything writes. The symptom when
// the header is missing: change a setting, press Back, and the browser
// answers from its own cache with the values from before the write -- which
// reads as "my change did not save" when the camera took it.
func TestRenderedPagesAreNeverStored(t *testing.T) {
	fsys := fstest.MapFS{
		"layout.html": &fstest.MapFile{Data: []byte(`{{define "layout"}}<p>{{template "body" .}}</p>{{end}}`)},
		"page.html":   &fstest.MapFile{Data: []byte(`{{define "body"}}hello{{end}}`)},
	}
	r, err := NewRenderer(fsys, "layout.html", nil)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	w := httptest.NewRecorder()
	r.Render(w, "page.html", nil)

	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Body.String(); got != "<p>hello</p>" {
		t.Errorf("body = %q", got)
	}
}
