package baichuan

import (
	"strings"
	"testing"
)

// A real ServerPort document from a TrackMix, RTSP off.
const netPortXML = `<?xml version="1.0" encoding="UTF-8" ?>
<body>
<ServerPort version="1.1"><serverPort>9000</serverPort><enable>1</enable></ServerPort>
<HttpPort version="1.1"><httpPort>80</httpPort><enable>1</enable></HttpPort>
<HttpsPort version="1.1"><httpsPort>443</httpsPort><enable>1</enable></HttpsPort>
<RtspPort version="1.1"><rtspPort>554</rtspPort><enable>0</enable></RtspPort>
<OnvifPort version="1.1"><onvifPort>8000</onvifPort><enable>1</enable></OnvifPort>
<RtmpPort version="1.1"><rtmpPort>1935</rtmpPort><enable>0</enable></RtmpPort>
</body>`

func TestParseNetPort(t *testing.T) {
	np, err := parseNetPort([]byte(netPortXML))
	if err != nil {
		t.Fatal(err)
	}
	if !np.HTTP.Enabled || np.HTTP.Port != 80 {
		t.Errorf("http = %+v", np.HTTP)
	}
	if np.RTSP.Enabled || np.RTSP.Port != 554 {
		t.Errorf("rtsp should read as off on 554: %+v", np.RTSP)
	}
	if !np.ONVIF.Enabled || np.RTMP.Enabled {
		t.Errorf("onvif on / rtmp off: onvif=%+v rtmp=%+v", np.ONVIF, np.RTMP)
	}
}

func TestRtspGroupDocEnablesRtspKeepsOthers(t *testing.T) {
	np, _ := parseNetPort([]byte(netPortXML)) // onvif on, rtmp off
	// Enable RTSP, preserve onvif (on) and rtmp (off).
	doc := string(streamGroupDoc(np, true, np.ONVIF.Enabled, np.RTMP.Enabled))
	// RTSP forced on, onvif/rtmp preserved, the three-port group present.
	for _, want := range []string{
		"<rtspPort>554</rtspPort>", "<onvifPort>8000</onvifPort>", "<rtmpPort>1935</rtmpPort>",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q in\n%s", want, doc)
		}
	}
	// RtspPort block must carry enable 1.
	rtsp := doc[strings.Index(doc, "<RtspPort"):strings.Index(doc, "</RtspPort>")]
	if !strings.Contains(rtsp, "<enable>1</enable>") {
		t.Errorf("rtsp not enabled: %s", rtsp)
	}
	onvif := doc[strings.Index(doc, "<OnvifPort"):strings.Index(doc, "</OnvifPort>")]
	if !strings.Contains(onvif, "<enable>1</enable>") {
		t.Errorf("onvif state not preserved (was on): %s", onvif)
	}
	rtmp := doc[strings.Index(doc, "<RtmpPort"):strings.Index(doc, "</RtmpPort>")]
	if !strings.Contains(rtmp, "<enable>0</enable>") {
		t.Errorf("rtmp state not preserved (was off): %s", rtmp)
	}
}

func TestRtspGroupDocDefaultsZeroPorts(t *testing.T) {
	doc := string(streamGroupDoc(NetPort{}, true, false, false)) // all zero
	for _, want := range []string{"<rtspPort>554</rtspPort>", "<onvifPort>8000</onvifPort>", "<rtmpPort>1935</rtmpPort>"} {
		if !strings.Contains(doc, want) {
			t.Errorf("zero port should default: missing %q", want)
		}
	}
}
