package cgi

import (
	"context"
	"encoding/json"
	"fmt"
)

// The camera's own noise: how loud it is, and whether it makes any when it
// sees something.
//
// What is here is what the cameras answer, which is less than the message
// table suggests. Both the fisheye and the pano report "audio alarm yes",
// and over CGI that buys exactly two commands: GetAudioCfg, which is a
// volume with a range, and GetAudioAlarmV20, which is an on/off plus a
// week-long schedule per kind of detection. Everything else that would make
// a siren page interesting -- the sound list, the custom upload, playing one
// on demand -- answers "not support" on both models, with rspCode -9.
//
// -9 and -1 are different answers and the difference is worth keeping: -1 is
// "no such command", -9 is "that command exists and this model does not do
// it". GetAudioFileList, GetAudioFileInfoList, GetAudioFileListV20,
// GetAlarmAudio, GetAudioTask, GetAudioTaskList, GetSiren, GetAudioPlay and
// GetBuzzerAlarmV20 all answered -9 on the fisheye, so that whole surface is
// real firmware that these cameras do not implement. Its Baichuan
// equivalents (347, 349, 261, 262) are a separate question and are not
// touched here.

// AudioCfg is the camera's speaker volume.
type AudioCfg struct {
	Volume int `json:"volume"`
}

// VolumeRange is what the camera says a volume may be. It is read rather
// than assumed for the same reason the encoder's stops are: 0 to 100 is what
// these two cameras answer, and a third camera has not been asked.
type VolumeRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// GetAudioCfg reads the speaker volume and the range it may take.
func (c *Client) GetAudioCfg(ctx context.Context, channel int) (AudioCfg, VolumeRange, error) {
	value, _, rng, err := c.Get(ctx, "GetAudioCfg", channel)
	if err != nil {
		return AudioCfg{}, VolumeRange{}, err
	}
	var v struct {
		AudioCfg AudioCfg `json:"AudioCfg"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return AudioCfg{}, VolumeRange{}, fmt.Errorf("cgi: parse audio cfg: %w", err)
	}
	var r struct {
		AudioCfg struct {
			Volume VolumeRange `json:"volume"`
		} `json:"AudioCfg"`
	}
	// A camera that answers the value but not the range is not an error:
	// the caller gets a zero range and offers no slider bounds of its own.
	_ = json.Unmarshal(rng, &r)
	return v.AudioCfg, r.AudioCfg.Volume, nil
}

// SetAudioCfg writes the speaker volume.
func (c *Client) SetAudioCfg(ctx context.Context, channel, volume int) error {
	return c.Set(ctx, "SetAudioCfg", map[string]any{
		"AudioCfg": map[string]any{"channel": channel, "volume": volume},
	})
}

// GetAudioAlarm reads the alarm sound's whole document.
//
// A map rather than a struct, the same discipline readFloodlightDoc uses:
// the document carries a schedule of 168 characters per detection type --
// one per hour of the week -- and the types differ by model. The fisheye
// sends AI_PEOPLE, AI_VEHICLE and MD; the pano sends crossline and intrusion
// entries instead. Nothing here models that, and nothing here may drop it,
// so the caller changes one field on top of what the camera sent.
func (c *Client) GetAudioAlarm(ctx context.Context, channel int) (map[string]any, error) {
	value, _, _, err := c.Get(ctx, "GetAudioAlarmV20", channel)
	if err != nil {
		return nil, err
	}
	var v struct {
		Audio map[string]any `json:"Audio"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return nil, fmt.Errorf("cgi: parse audio alarm: %w", err)
	}
	if v.Audio == nil {
		return nil, fmt.Errorf("cgi: GetAudioAlarmV20 carried no Audio document")
	}
	return v.Audio, nil
}

// AudioAlarmEnabled reads the on/off out of that document.
func AudioAlarmEnabled(doc map[string]any) bool {
	f, ok := doc["enable"].(float64)
	return ok && f != 0
}

// SetAudioAlarmEnabled turns the alarm sound on or off, writing the
// camera's own document back with that one field changed -- never a
// composed one, which would drop the schedule the camera keeps in it.
func (c *Client) SetAudioAlarmEnabled(ctx context.Context, channel int, doc map[string]any, on bool) error {
	next := make(map[string]any, len(doc)+1)
	for k, v := range doc {
		next[k] = v
	}
	if on {
		next["enable"] = 1
	} else {
		next["enable"] = 0
	}
	return c.Set(ctx, "SetAudioAlarmV20", map[string]any{"Audio": next})
}
