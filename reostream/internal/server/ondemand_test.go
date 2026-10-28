package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VoltTech21/reostream/internal/hub"
)

func batteryHubs() HubSource {
	h := hub.New(4)
	h.SetOnDemand(true)
	return StaticHubs(map[string]*hub.Hub{"porch/sub": h})
}

// The public listener must not let a recorder wake a battery camera: one
// pulling it around the clock would flatten the battery in a day.
func TestPublicListenerRefusesABatteryStream(t *testing.T) {
	rec := httptest.NewRecorder()
	New(batteryHubs()).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/porch_sub.ts", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
}

func TestSignedInListenerMayWakeABatteryStream(t *testing.T) {
	rec := httptest.NewRecorder()
	New(batteryHubs()).AllowWake().StreamHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/porch_sub.ts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func TestStillIsTheLastKeyframeAsMPEGTS(t *testing.T) {
	hubs := batteryHubs()
	h, _ := hubs.Hub("porch/sub")
	srv := New(hubs).StreamHandler()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/porch_sub.still.ts", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no keyframe yet: status %d, want 404", rec.Code)
	}

	h.SetKeyframe("H264", []byte{0, 0, 0, 1, 0x67, 0x64, 0, 0x1f, 0, 0, 0, 1, 0x68, 0xee, 0, 0, 0, 1, 0x65, 0x88, 0x84})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/porch_sub.still.ts", nil))
	b := rec.Body.Bytes()
	if rec.Code != http.StatusOK || len(b) < 188*3 || len(b)%188 != 0 || b[0] != 0x47 {
		t.Fatalf("status %d, %d bytes: not a TS of header and frame", rec.Code, len(b))
	}
	if h.Clients() != 0 {
		t.Fatal("serving a still subscribed to the stream, which would wake the camera")
	}
}
