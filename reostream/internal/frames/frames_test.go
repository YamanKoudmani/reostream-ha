package frames

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

func TestRecordRoundTrip(t *testing.T) {
	in := Record{Kind: KindKey, Codec: CodecH265, Width: 1536, Height: 432, Micros: 123456789, Payload: []byte{0, 0, 0, 1, 0x40, 1, 2}}
	out, err := ReadRecord(bytes.NewReader(in.Marshal()))
	if err != nil {
		t.Fatal(err)
	}
	if out.Kind != in.Kind || out.Codec != in.Codec || out.Width != in.Width || out.Height != in.Height || out.Micros != in.Micros || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("round trip changed the record: %+v -> %+v", in, out)
	}
}

func key(micros uint32) baichuan.Frame {
	return baichuan.Frame{Kind: baichuan.FrameIFrame, Codec: "H264", Micros: micros, Data: []byte{0, 0, 0, 1, 0x65, 1}}
}

func inter(micros uint32) baichuan.Frame {
	return baichuan.Frame{Kind: baichuan.FramePFrame, Codec: "H264", Micros: micros, Data: []byte{0, 0, 0, 1, 0x41, 1}}
}

// A decoder cannot start on an inter frame, so a subscriber's first record
// must be a keyframe whatever was flowing when it joined.
func TestSubscriberStartsAtAKeyframe(t *testing.T) {
	st := newStream()
	sub, cancel := st.subscribe()
	defer cancel()
	st.Frame(inter(1))
	st.Frame(inter(2))
	st.Frame(key(3))
	st.Frame(inter(4))
	if len(sub.ch) != 2 {
		t.Fatalf("subscriber holds %d records, want 2 (the keyframe and the frame after it)", len(sub.ch))
	}
	rec, _ := ReadRecord(bytes.NewReader(<-sub.ch))
	if rec.Kind != KindKey || rec.Micros != 3 {
		t.Fatalf("first record kind %d micros %d, want the keyframe at 3", rec.Kind, rec.Micros)
	}
}

// A subscriber that fell behind is resumed at the next keyframe, never
// handed an inter frame whose references it dropped.
func TestSlowSubscriberResumesAtAKeyframe(t *testing.T) {
	st := newStream()
	sub, cancel := st.subscribe()
	defer cancel()
	st.Frame(key(0))
	for i := 1; i < subscriberBuffer+10; i++ {
		st.Frame(inter(uint32(i)))
	}
	// Drain, then see what comes next.
	for len(sub.ch) > 0 {
		<-sub.ch
	}
	st.Frame(inter(1000))
	if len(sub.ch) != 0 {
		t.Fatal("an inter frame was delivered after records were dropped")
	}
	st.Frame(key(1001))
	st.Frame(inter(1002))
	if len(sub.ch) != 2 {
		t.Fatalf("got %d records after the keyframe, want 2", len(sub.ch))
	}
}

// The size arrives in the camera's info packet, not on the frames, and is
// stamped on every record so a client can configure its decoder from the
// first one.
func TestSizeComesFromTheInfoPacket(t *testing.T) {
	st := newStream()
	sub, cancel := st.subscribe()
	defer cancel()
	st.Frame(baichuan.Frame{Kind: baichuan.FrameInfo, Width: 1536, Height: 432})
	st.Frame(key(0))
	rec, _ := ReadRecord(bytes.NewReader(<-sub.ch))
	if rec.Width != 1536 || rec.Height != 432 {
		t.Fatalf("record size %dx%d, want 1536x432", rec.Width, rec.Height)
	}
}

// Nothing is marshalled for nobody: the camera goroutine pays for this feed
// only while someone is reading it.
func TestNoWorkWithoutSubscribers(t *testing.T) {
	st := newStream()
	st.Frame(key(0)) // must simply not panic or allocate records; nothing to assert beyond running
}

func TestServeHTTPStreamsRecords(t *testing.T) {
	srv := New()
	st := srv.Add("pano_sub")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/pano_sub.frames")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// The subscriber registers inside the handler, after the request is
	// accepted; give it a moment before feeding.
	deadline := time.Now().Add(2 * time.Second)
	for {
		st.mu.Lock()
		n := len(st.subs)
		st.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	st.Frame(inter(1))
	st.Frame(key(2))
	st.Frame(inter(3))

	r1, err := ReadRecord(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ReadRecord(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Kind != KindKey || r1.Micros != 2 || r2.Kind != KindInter || r2.Micros != 3 {
		t.Fatalf("got %+v then %+v", r1, r2)
	}

	resp2, _ := http.Get(ts.URL + "/nothing.frames")
	if resp2.StatusCode != 404 {
		t.Fatalf("unknown path status %d, want 404", resp2.StatusCode)
	}
	resp2.Body.Close()
	_ = io.EOF
}
