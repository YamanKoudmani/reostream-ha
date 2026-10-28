package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VoltTech21/reostream/internal/cgi"
)

// Factory defaults, for the camera page's reset buttons.
//
// A CGI read with action 1 answers with the camera's current value, its
// factory default ("initial") and the valid range together, which is how
// this page learns a default without keeping a table of its own: every
// value offered here is one the camera itself reported. The curated
// settings are Baichuan documents, so each default is mapped onto the
// XPath the page edits. Only fields where that mapping is one to one are
// mapped. The LED is not: its CGI values do not line up one to one with
// the Baichuan document's.

// cgiCorner is the CGI API's name for each corner an overlay can sit in,
// spaces removed: an RLC-810A reports "LowerRight" and "TopCenter".
// Bottom center has no Baichuan pair, so a camera defaulting there gets no
// reset.
var cgiCorner = map[string]string{
	"UpperLeft":   "top left",
	"TopLeft":     "top left",
	"TopCenter":   "top center",
	"UpperCenter": "top center",
	"UpperRight":  "top right",
	"TopRight":    "top right",
	"LowerLeft":   "bottom left",
	"BottomLeft":  "bottom left",
	"LowerRight":  "bottom right",
	"BottomRight": "bottom right",
}

// cgiDayNight is the CGI spelling of each day and night mode, mapped to
// the Baichuan document's.
var cgiDayNight = map[string]string{
	"Auto":        "auto",
	"Color":       "color",
	"Black&White": "blackAndWhite",
}

// readDefaults asks the camera for the factory values of the curated fields
// it can map, keyed by each field's primary XPath. Any read that fails
// simply contributes nothing.
func readDefaults(ctx context.Context, c *cgi.Client, channel int) map[string]string {
	out := map[string]string{}

	if _, initial, _, err := c.Get(ctx, "GetImage", channel); err == nil {
		var v struct {
			Image map[string]json.Number `json:"Image"`
		}
		if json.Unmarshal(initial, &v) == nil {
			for cgiKey, xpath := range map[string]string{
				"bright":     "VideoInput/bright",
				"contrast":   "VideoInput/contrast",
				"saturation": "VideoInput/saturation",
			} {
				if n, ok := v.Image[cgiKey]; ok {
					out[xpath] = n.String()
				}
			}
		}
	}

	if _, initial, _, err := c.Get(ctx, "GetOsd", channel); err == nil {
		var v struct {
			Osd struct {
				OsdChannel *struct {
					Enable *int   `json:"enable"`
					Pos    string `json:"pos"`
				} `json:"osdChannel"`
				OsdTime *struct {
					Enable *int   `json:"enable"`
					Pos    string `json:"pos"`
				} `json:"osdTime"`
				Watermark *int `json:"watermark"`
			} `json:"Osd"`
		}
		if json.Unmarshal(initial, &v) == nil {
			if v.Osd.Watermark != nil {
				out["OsdChannelName/enWatermark"] = fmt.Sprint(*v.Osd.Watermark)
			}
			if ch := v.Osd.OsdChannel; ch != nil {
				if ch.Enable != nil {
					out["OsdChannelName/enable"] = fmt.Sprint(*ch.Enable)
				}
				if xy, ok := cornerPair(ch.Pos); ok {
					out["OsdChannelName/topLeftX"] = xy
				}
			}
			if t := v.Osd.OsdTime; t != nil {
				if t.Enable != nil {
					out["OsdDatetime/enable"] = fmt.Sprint(*t.Enable)
				}
				if xy, ok := cornerPair(t.Pos); ok {
					out["OsdDatetime/topLeftX"] = xy
				}
			}
		}
	}

	if _, initial, _, err := c.Get(ctx, "GetIsp", channel); err == nil {
		var v struct {
			Isp struct {
				DayNight  string `json:"dayNight"`
				Mirroring *int   `json:"mirroring"`
				Rotation  *int   `json:"rotation"`
			} `json:"Isp"`
		}
		if json.Unmarshal(initial, &v) == nil {
			if mode, ok := cgiDayNight[v.Isp.DayNight]; ok {
				out["InputAdvanceCfg/DayNight/mode"] = mode
			}
			// mirroring and rotation track the document's mirror and flip
			// exactly: 1,1 on a ceiling-mounted TrackMix, 0,0 on an
			// RLC-810A, through both protocols.
			if v.Isp.Mirroring != nil {
				out["InputAdvanceCfg/mirror"] = fmt.Sprint(*v.Isp.Mirroring)
			}
			if v.Isp.Rotation != nil {
				out["InputAdvanceCfg/flip"] = fmt.Sprint(*v.Isp.Rotation)
			}
		}
	}
	return out
}

// cornerPair turns a CGI position name into the "x,y" pair the position
// control posts.
func cornerPair(pos string) (string, bool) {
	label, ok := cgiCorner[strings.ReplaceAll(pos, " ", "")]
	if !ok {
		return "", false
	}
	for _, c := range osdCorners() {
		if c.Label == label {
			return c.X + "," + c.Y, true
		}
	}
	return "", false
}
