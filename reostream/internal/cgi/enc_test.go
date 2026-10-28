package cgi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The pano's own answer, captured 2026-10-13. Kept verbatim rather than
// tidied: the shape is the thing being tested, and the detail that matters
// most -- the range being an ARRAY of whole combinations, one per
// resolution -- is invisible in a reduced fixture.
const panoEncValue = `{"Enc":{"audio":1,"channel":0,` +
	`"mainStream":{"bitRate":10240,"frameRate":20,"gop":1,"height":1152,"profile":"High","size":"4096*1152","vType":"h265","width":4096},` +
	`"subStream":{"bitRate":2048,"frameRate":20,"gop":1,"height":432,"profile":"High","size":"1536*432","vType":"h264","width":1536}}}`

const panoEncRange = `{"Enc":[` +
	`{"audio":"boolean","chnBit":1,` +
	`"mainStream":{"bitRate":[3072,4096,5120,6144,7168,8192,9216,10240,12288],"default":{"bitRate":10240,"frameRate":20,"gop":2},` +
	`"frameRate":[20,18,16,15,12,10,8,6,4],"gop":{"max":2,"min":1},` +
	`"height":2160,"profile":["Base","Main","High"],"size":"7680*2160","vType":["h265"],"width":7680},` +
	`"subStream":{"bitRate":[256,512,1024,1536,2048],"default":{"bitRate":1024,"frameRate":20,"gop":4},` +
	`"frameRate":[20,15,10,7,4],"gop":{"max":4,"min":1},` +
	`"height":432,"profile":["Base","Main","High"],"size":"1536*432","vType":["h264"],"width":1536}},` +
	`{"audio":"boolean","chnBit":1,` +
	`"mainStream":{"bitRate":[3072,4096,5120,6144,7168,8192,9216,10240],"default":{"bitRate":8192,"frameRate":20,"gop":2},` +
	`"frameRate":[20,18,16,15,12,10,8,6,4,2],"gop":{"max":2,"min":1},` +
	`"height":1152,"profile":["Base","Main","High"],"size":"4096*1152","vType":["h265"],"width":4096},` +
	`"subStream":{"bitRate":[256,512,1024,1536,2048],"default":{"bitRate":1024,"frameRate":20,"gop":4},` +
	`"frameRate":[20,15,10,7,4],"gop":{"max":4,"min":1},` +
	`"height":432,"profile":["Base","Main","High"],"size":"1536*432","vType":["h264"],"width":1536}}]}`

func encServer(t *testing.T, value, rng string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cmd") {
		case "Login":
			w.Write([]byte(loginReply("tok")))
		case "Logout":
			w.Write([]byte(`[{"cmd":"Logout","code":0,"value":{"rspCode":200}}]`))
		case "GetEnc":
			w.Write([]byte(`[{"cmd":"GetEnc","code":0,"value":` + value + `,"range":` + rng + `}]`))
		default:
			t.Errorf("unexpected cmd %q", r.URL.Query().Get("cmd"))
		}
	}))
	t.Cleanup(srv.Close)
	c, err := Dial(strings.TrimPrefix(srv.URL, "http://"), "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestGetEncReadsTheValueAndEveryCombination(t *testing.T) {
	c := encServer(t, panoEncValue, panoEncRange)
	cur, opts, err := c.GetEnc(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetEnc: %v", err)
	}

	if cur.Main.Size != "4096*1152" || cur.Main.BitRate != 10240 || cur.Main.GOP != 1 {
		t.Errorf("main = %+v", cur.Main)
	}
	if cur.Main.VType != "h265" {
		t.Errorf("main vType = %q, want h265", cur.Main.VType)
	}
	if cur.Sub.Size != "1536*432" || cur.Sub.BitRate != 2048 {
		t.Errorf("sub = %+v", cur.Sub)
	}

	// Two whole combinations, not one list of sizes and one of bitrates.
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2", len(opts))
	}
	if opts[0].Main.Size != "7680*2160" || opts[1].Main.Size != "4096*1152" {
		t.Fatalf("sizes = %q and %q", opts[0].Main.Size, opts[1].Main.Size)
	}

	// And the reason they cannot be flattened: 12288 is offered at
	// 7680x2160 and NOT at 4096x1152. A form that merged these would
	// present a bitrate this camera refuses at the size it is set to.
	if !hasInt(opts[0].Main.BitRates, 12288) {
		t.Error("7680*2160 should offer 12288")
	}
	if hasInt(opts[1].Main.BitRates, 12288) {
		t.Error("4096*1152 must NOT offer 12288")
	}
	// Likewise frame rate: 2 fps exists only at the smaller size.
	if hasInt(opts[0].Main.FrameRates, 2) {
		t.Error("7680*2160 should not offer 2 fps")
	}
	if !hasInt(opts[1].Main.FrameRates, 2) {
		t.Error("4096*1152 should offer 2 fps")
	}

	if opts[1].Main.GOPMin != 1 || opts[1].Main.GOPMax != 2 {
		t.Errorf("main gop range = %d..%d, want 1..2", opts[1].Main.GOPMin, opts[1].Main.GOPMax)
	}
	if opts[1].Main.DefaultBitRate != 8192 {
		t.Errorf("4096*1152 default bitrate = %d, want 8192", opts[1].Main.DefaultBitRate)
	}
	if len(opts[1].Main.VTypes) != 1 || opts[1].Main.VTypes[0] != "h265" {
		t.Errorf("main vTypes = %v; the main stream is HEVC only on this camera", opts[1].Main.VTypes)
	}
}

// A camera offering one combination may reasonably answer with an object
// rather than a one-element array. Falling back is cheaper than leaving a
// page with no options at all.
func TestGetEncAcceptsASingleCombinationAsAnObject(t *testing.T) {
	rng := `{"Enc":{"mainStream":{"bitRate":[1024,2048],"frameRate":[20,10],` +
		`"gop":{"max":2,"min":1},"height":1152,"profile":["High"],"size":"4096*1152",` +
		`"vType":["h265"],"width":4096},` +
		`"subStream":{"bitRate":[256],"frameRate":[20],"gop":{"max":4,"min":1},` +
		`"height":432,"profile":["High"],"size":"1536*432","vType":["h264"],"width":1536}}}`

	c := encServer(t, panoEncValue, rng)
	_, opts, err := c.GetEnc(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetEnc: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("options = %d, want 1", len(opts))
	}
	if opts[0].Main.Size != "4096*1152" || len(opts[0].Main.BitRates) != 2 {
		t.Errorf("main options = %+v", opts[0].Main)
	}
}

// SetEnc sends the whole block. A partial write is how a stream ends up at
// one resolution carrying another's bitrate.
func TestSetEncSendsEveryFieldOfBothStreams(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("cmd") {
		case "Login":
			w.Write([]byte(loginReply("tok")))
		case "SetEnc":
			var req []struct {
				Param map[string]any `json:"param"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("bad SetEnc body: %v", err)
			} else if len(req) == 1 {
				got, _ = req[0].Param["Enc"].(map[string]any)
			}
			w.Write([]byte(`[{"cmd":"SetEnc","code":0}]`))
		default:
			t.Errorf("unexpected cmd %q", r.URL.Query().Get("cmd"))
		}
	}))
	defer srv.Close()

	c, err := Dial(strings.TrimPrefix(srv.URL, "http://"), "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	e := Enc{
		Audio: 1,
		Main:  EncStream{BitRate: 8192, FrameRate: 15, GOP: 2, Width: 4096, Height: 1152, Size: "4096*1152", Profile: "High", VType: "h265"},
		Sub:   EncStream{BitRate: 1024, FrameRate: 10, GOP: 4, Width: 1536, Height: 432, Size: "1536*432", Profile: "High", VType: "h264"},
	}
	if err := c.SetEnc(context.Background(), 0, e); err != nil {
		t.Fatalf("SetEnc: %v", err)
	}

	main, _ := got["mainStream"].(map[string]any)
	if main == nil {
		t.Fatalf("no mainStream in %v", got)
	}
	for _, k := range []string{"bitRate", "frameRate", "gop", "width", "height", "size", "profile", "vType"} {
		if _, ok := main[k]; !ok {
			t.Errorf("mainStream is missing %q", k)
		}
	}
	if main["size"] != "4096*1152" {
		t.Errorf("size = %v", main["size"])
	}
	// The size string and its numbers must agree, because a camera was
	// never asked to interpret a pair that disagrees.
	if main["width"] != float64(4096) || main["height"] != float64(1152) {
		t.Errorf("width/height = %v/%v, do not match size", main["width"], main["height"])
	}
	if _, ok := got["subStream"].(map[string]any); !ok {
		t.Error("subStream was not sent")
	}
}

func hasInt(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// The RLC-810A sends a stream's codec range as one bare string.
func TestStringListTakesABareString(t *testing.T) {
	var j encStreamRangeJSON
	if err := json.Unmarshal([]byte(`{"vType":"h265","profile":["High","Main"]}`), &j); err != nil {
		t.Fatalf("a bare-string vType did not decode: %v", err)
	}
	if len(j.VType) != 1 || j.VType[0] != "h265" || len(j.Profile) != 2 {
		t.Fatalf("got vType %v profile %v", j.VType, j.Profile)
	}
}
