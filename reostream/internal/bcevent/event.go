// Package bcevent interprets the cloud event reports a battery camera emits
// (bcudp cf31 packets) as motion, recording and status. A sniffer sitting on
// the camera's path to its cloud server can decode these in real time, which
// gives a near-instant local motion signal — the camera sends event 3 the
// moment it wakes on motion, long before an FTP upload or an SD recording is
// finished — without the camera's cloud account or any change to the camera.
package bcevent

import "encoding/json"

// Kind is the sort of report an event number names.
type Kind string

const (
	Motion       Kind = "motion"        // event 3: the camera woke on motion
	RecordingEnd Kind = "recording_end" // event 4: a clip finished (carries duration)
	Status       Kind = "status"        // event 12: battery, power, wifi, time
	Codec        Kind = "codec"         // event 8: stream parameters
	Firmware     Kind = "firmware"      // event 0: firmware/summary
	Unknown      Kind = "unknown"
)

// Battery is the battery sub-object of a status report. Current is milliamps,
// negative while discharging; Millivolts is the pack voltage; TempC is the
// pack temperature in Celsius; Pct is the state of charge.
type Battery struct {
	Current     int `json:"bc"`
	Millivolts  int `json:"bv"`
	TempC       int `json:"tm"`
	ChargeState int `json:"cs"`
	Low         int `json:"low"`
	Pct         int `json:"pct"`
}

// Report is a decoded event. Fields outside the one that matched Kind are
// left at their zero value; Raw holds the original JSON for anything not
// modeled here.
type Report struct {
	Event int             `json:"event"`
	Kind  Kind            `json:"-"`
	Raw   json.RawMessage `json:"-"`

	// RecordingEnd (event 4).
	DurationMS    int `json:"duration"`
	TotalDuration int `json:"totalduration"`
	Reason        int `json:"reason"`

	// Status (event 12).
	HostPwr  int      `json:"hostpwr"`
	Battery  *Battery `json:"bat"`
	WifiRSSI int      `json:"wifi_rssi"`
	Time     int64    `json:"time"`
}

// Parse decodes one event body (the JSON from a bcudp.Event) and classifies
// it. The body is kept verbatim in Raw.
func Parse(body []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(body, &r); err != nil {
		return Report{}, err
	}
	r.Raw = append(json.RawMessage(nil), body...)
	switch r.Event {
	case 3:
		r.Kind = Motion
	case 4:
		r.Kind = RecordingEnd
	case 12:
		r.Kind = Status
	case 8:
		r.Kind = Codec
	case 0:
		r.Kind = Firmware
	default:
		r.Kind = Unknown
	}
	return r, nil
}

// Awake reports whether a status event shows the camera's main SoC powered
// (recording or otherwise busy) rather than back in low-power sleep.
func (r Report) Awake() bool { return r.HostPwr != 0 }
