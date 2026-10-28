package cgi

import (
	"context"
	"encoding/json"
	"fmt"
)

// EncStream is one stream's encoder settings.
//
// Size is the camera's own spelling of the resolution, "4096*1152", with a
// star rather than an x. Width and Height repeat it as numbers; the camera
// sends all three and SetEnc is given all three back, because a size string
// that disagrees with its own numbers is a value no camera was ever asked
// to interpret.
//
// GOP is the interframe space, which is the vendor's own label for it: how
// many frames pass between keyframes. The camera reports it in frames and
// the range is small -- 1 to 2 on a main stream here -- so this is not the
// dozens-of-frames GOP of a streaming encoder.
type EncStream struct {
	BitRate   int    `json:"bitRate"`
	FrameRate int    `json:"frameRate"`
	GOP       int    `json:"gop"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Size      string `json:"size"`
	Profile   string `json:"profile"`
	VType     string `json:"vType"`
}

// Enc is a channel's encoder settings: one block per stream, plus whether
// audio is encoded at all.
type Enc struct {
	Audio  int       `json:"audio"`
	Main   EncStream `json:"mainStream"`
	Sub    EncStream `json:"subStream"`
	Extern EncStream `json:"extStream"`
}

// EncStreamOptions is what one stream will accept, as the camera reports
// it rather than as anything here assumes.
//
// BitRates and FrameRates are the exact stops offered; a value not in the
// list is refused by the camera. Profiles and VTypes likewise. GOPMin and
// GOPMax bound the interframe space.
//
// Size is the resolution these options belong to, which is the subtlety of
// this whole structure: see EncOptions.
type EncStreamOptions struct {
	Size       string
	Width      int
	Height     int
	BitRates   []int
	FrameRates []int
	GOPMin     int
	GOPMax     int
	Profiles   []string
	VTypes     []string

	// Default is what the camera calls the default for this size, which is
	// not the same as the value in force.
	DefaultBitRate   int
	DefaultFrameRate int
	DefaultGOP       int
}

// EncOptions is one whole combination the camera offers.
//
// The camera answers GetEnc's range with an ARRAY of these, one per
// resolution it supports, and the bitrates and frame rates inside each are
// only valid for that resolution. On the pano, 7680x2160 offers bitrates up
// to 12288 and 4096x1152 stops at 10240. So "what bitrates may I choose" has
// no answer until a resolution is chosen, and a form that presents the two
// as independent lists will offer combinations the camera refuses.
type EncOptions struct {
	Main EncStreamOptions
	Sub  EncStreamOptions
}

// encStreamRangeJSON is the wire shape of one stream's options. Kept
// separate from EncStreamOptions so the JSON spelling stays in one place.
type encStreamRangeJSON struct {
	BitRate   []int                  `json:"bitRate"`
	FrameRate []int                  `json:"frameRate"`
	Profile   stringList             `json:"profile"`
	VType     stringList             `json:"vType"`
	Size      string                 `json:"size"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	GOP       struct{ Min, Max int } `json:"gop"`
	Default   struct {
		BitRate   int `json:"bitRate"`
		FrameRate int `json:"frameRate"`
		GOP       int `json:"gop"`
	} `json:"default"`
}

// stringList is a list the camera may also send as one bare string: the
// RLC-810A's range says "vType": "h265" where the pano says ["h264","h265"],
// and a strict []string turned that into a decode error that left the
// whole Encoding page blank.
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

func (j encStreamRangeJSON) options() EncStreamOptions {
	return EncStreamOptions{
		Size:             j.Size,
		Width:            j.Width,
		Height:           j.Height,
		BitRates:         j.BitRate,
		FrameRates:       j.FrameRate,
		GOPMin:           j.GOP.Min,
		GOPMax:           j.GOP.Max,
		Profiles:         j.Profile,
		VTypes:           j.VType,
		DefaultBitRate:   j.Default.BitRate,
		DefaultFrameRate: j.Default.FrameRate,
		DefaultGOP:       j.Default.GOP,
	}
}

// GetEnc reads the encoder settings and every combination the camera says
// it will accept.
//
// The range is the point of this call. Without it a form has to offer free
// number boxes and learn what the camera refuses by refusing to work, and
// the fields are not independent -- see EncOptions.
func (c *Client) GetEnc(ctx context.Context, channel int) (current Enc, options []EncOptions, err error) {
	value, _, rng, err := c.Get(ctx, "GetEnc", channel)
	if err != nil {
		return Enc{}, nil, err
	}

	var cur struct {
		Enc Enc `json:"Enc"`
	}
	if err := json.Unmarshal(value, &cur); err != nil {
		return Enc{}, nil, fmt.Errorf("cgi: parse GetEnc: %w", err)
	}

	// The range is an array on every camera measured, but a camera that
	// offers exactly one combination could reasonably send an object. Try
	// the array first and fall back, rather than failing the whole read and
	// leaving the page with no options at all.
	var asArray struct {
		Enc []struct {
			Main encStreamRangeJSON `json:"mainStream"`
			Sub  encStreamRangeJSON `json:"subStream"`
		} `json:"Enc"`
	}
	if err := json.Unmarshal(rng, &asArray); err == nil && len(asArray.Enc) > 0 {
		for _, e := range asArray.Enc {
			options = append(options, EncOptions{Main: e.Main.options(), Sub: e.Sub.options()})
		}
		return cur.Enc, options, nil
	}

	var asObject struct {
		Enc struct {
			Main encStreamRangeJSON `json:"mainStream"`
			Sub  encStreamRangeJSON `json:"subStream"`
		} `json:"Enc"`
	}
	if err := json.Unmarshal(rng, &asObject); err == nil && asObject.Enc.Main.Size != "" {
		options = append(options, EncOptions{
			Main: asObject.Enc.Main.options(),
			Sub:  asObject.Enc.Sub.options(),
		})
	}
	return cur.Enc, options, nil
}

// SetEnc writes the encoder settings.
//
// This re-encodes what the camera records and what every reader of it
// receives. It is not a reboot like a fisheye mode change, but a recorder
// holding the stream will see it stop and come back with different
// parameters, which for an H.264 or H.265 consumer means waiting for the
// next keyframe at best and a reconnect at worst.
//
// Every field is sent, not only the changed one. The camera takes the whole
// block, and a partial write is how a stream ends up at a resolution with a
// bitrate that belongs to a different one.
func (c *Client) SetEnc(ctx context.Context, channel int, e Enc) error {
	stream := func(s EncStream) map[string]any {
		return map[string]any{
			"bitRate":   s.BitRate,
			"frameRate": s.FrameRate,
			"gop":       s.GOP,
			"width":     s.Width,
			"height":    s.Height,
			"size":      s.Size,
			"profile":   s.Profile,
			"vType":     s.VType,
		}
	}
	return c.Set(ctx, "SetEnc", map[string]any{
		"Enc": map[string]any{
			"channel":    channel,
			"audio":      e.Audio,
			"mainStream": stream(e.Main),
			"subStream":  stream(e.Sub),
		},
	})
}
