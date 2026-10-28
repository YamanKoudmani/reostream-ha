package bcevent

import "testing"

func TestParseClassifiesAndReadsFields(t *testing.T) {
	m, err := Parse([]byte(`{"event":3}`))
	if err != nil || m.Kind != Motion {
		t.Fatalf("motion: kind=%q err=%v", m.Kind, err)
	}

	r, err := Parse([]byte(`{"event":4,"duration":25283,"totalduration":2120071,"reason":5}`))
	if err != nil || r.Kind != RecordingEnd {
		t.Fatalf("recording: kind=%q err=%v", r.Kind, err)
	}
	if r.DurationMS != 25283 || r.Reason != 5 {
		t.Errorf("recording fields = %+v", r)
	}

	s, err := Parse([]byte(`{"event":12,"hostpwr":1,"wifi_rssi":-32,"time":1792764262,"bat":{"bc":-329,"bv":4041,"tm":29,"cs":3,"low":0,"pct":79}}`))
	if err != nil || s.Kind != Status {
		t.Fatalf("status: kind=%q err=%v", s.Kind, err)
	}
	if s.Battery == nil || s.Battery.Pct != 79 || s.Battery.TempC != 29 || s.WifiRSSI != -32 {
		t.Errorf("status fields = %+v bat=%+v", s, s.Battery)
	}
	if !s.Awake() {
		t.Error("hostpwr 1 should read as awake")
	}

	u, _ := Parse([]byte(`{"event":99}`))
	if u.Kind != Unknown {
		t.Errorf("unknown event kind = %q", u.Kind)
	}
}
