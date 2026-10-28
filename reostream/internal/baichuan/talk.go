package baichuan

import (
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
)

// TalkFormat is one audio configuration a camera will accept for two-way
// audio, as reported in its TalkAbility reply.
type TalkFormat struct {
	Duplex           string
	StreamMode       string
	AudioType        string
	SampleRate       int
	SamplePrecision  int
	LengthPerEncoder int
	SoundTrack       string
}

// talkAbilityReply is the camera's answer to MsgIDTalkAbility.
type talkAbilityReply struct {
	XMLName xml.Name `xml:"body"`
	Ability struct {
		DuplexList struct {
			Duplex []string `xml:"duplex"`
		} `xml:"duplexList"`
		StreamModes struct {
			Mode []string `xml:"audioStreamMode"`
		} `xml:"audioStreamModeList"`
		Configs struct {
			Config []struct {
				Priority         int    `xml:"priority"`
				AudioType        string `xml:"audioType"`
				SampleRate       int    `xml:"sampleRate"`
				SamplePrecision  int    `xml:"samplePrecision"`
				LengthPerEncoder int    `xml:"lengthPerEncoder"`
				SoundTrack       string `xml:"soundTrack"`
			} `xml:"audioConfig"`
		} `xml:"audioConfigList"`
	} `xml:"TalkAbility"`
}

// ParseTalkAbility reads a TalkAbility reply and returns the camera's
// preferred format: the first duplex mode, the first stream mode and the
// audioConfig with the lowest priority value, which is how the cameras rank
// them.
func ParseTalkAbility(x []byte) (TalkFormat, error) {
	var r talkAbilityReply
	if err := xml.Unmarshal(x, &r); err != nil {
		return TalkFormat{}, fmt.Errorf("baichuan: parse talk ability: %w", err)
	}
	if len(r.Ability.Configs.Config) == 0 {
		return TalkFormat{}, fmt.Errorf("baichuan: camera offered no talk audio config")
	}
	best := 0
	for i, c := range r.Ability.Configs.Config {
		if c.Priority < r.Ability.Configs.Config[best].Priority {
			best = i
		}
	}
	c := r.Ability.Configs.Config[best]

	f := TalkFormat{
		AudioType:        c.AudioType,
		SampleRate:       c.SampleRate,
		SamplePrecision:  c.SamplePrecision,
		LengthPerEncoder: c.LengthPerEncoder,
		SoundTrack:       c.SoundTrack,
	}
	if len(r.Ability.DuplexList.Duplex) > 0 {
		f.Duplex = r.Ability.DuplexList.Duplex[0]
	}
	if len(r.Ability.StreamModes.Mode) > 0 {
		f.StreamMode = r.Ability.StreamModes.Mode[0]
		// The Reolink app asks for mixAudioStream when it is offered
		// (TrackMix capture, 10-24), not the first mode listed
		// (followVideoStream).
		for _, m := range r.Ability.StreamModes.Mode {
			if m == "mixAudioStream" {
				f.StreamMode = m
			}
		}
	}
	return f, nil
}

// adpcmInnerMagic precedes the DVI4 block inside an audio media packet. The
// dissector records two values here, 0x0001 and 0x007a; 0x0001 is what a
// client sends.
var adpcmInnerMagic = []byte{0x00, 0x01}

// TalkPacket wraps one encoded ADPCM block in the media packet framing the
// cameras use, which is the same framing they send audio back in: the magic,
// the payload length twice, then a four byte inner header naming the DVI4
// block size.
//
// The doubled length is not a typo. It is doubled on the wire in both
// directions; a capture of a camera's own AAC shows the two fields carrying
// the same value.
func TalkPacket(block []byte) []byte {
	const innerHeader = 4
	payload := innerHeader + len(block)

	out := make([]byte, 0, 8+payload)
	out = append(out, magicADPCM...)
	out = binary.LittleEndian.AppendUint16(out, uint16(payload))
	out = binary.LittleEndian.AppendUint16(out, uint16(payload))
	out = append(out, adpcmInnerMagic...)
	// The block size field counts the packed nibbles, not the four byte
	// preamble that carries the decoder's starting state.
	out = binary.LittleEndian.AppendUint16(out, uint16(len(block)-4))
	out = append(out, block...)
	return out
}

// ErrNoSpeaker is OpenTalk refusing a camera whose Support block says it has
// nothing to play sound through.
var ErrNoSpeaker = errors.New("baichuan: this camera has no speaker (Support reports audioTalk 0)")

// OpenTalk opens a two-way audio session on conn and returns the format the
// camera agreed to, which every Talk packet on conn must then be in.
//
// The speaker check comes first and is not optional. A camera without one
// accepts the configuration with a 200, plays nothing, and then refuses every
// later session until it reboots, so asking costs one round trip and not
// asking can cost the feature on that camera until someone power cycles it.
func OpenTalk(ctx context.Context, conn *Conn) (TalkFormat, error) {
	sup, err := GetSupport(ctx, conn)
	if err != nil {
		return TalkFormat{}, fmt.Errorf("baichuan: talk: could not check for a speaker: %w", err)
	}
	if !sup.CanTalk() {
		return TalkFormat{}, ErrNoSpeaker
	}

	if err := conn.TalkAbility(); err != nil {
		return TalkFormat{}, err
	}
	var f TalkFormat
	for {
		// The camera can answer the ability query with an empty message
		// before the document; only the one with a body is the answer.
		m, err := awaitMessage(ctx, conn, MsgIDTalkAbility)
		if err != nil {
			return TalkFormat{}, err
		}
		if len(m.XML) == 0 {
			continue
		}
		if f, err = ParseTalkAbility(m.XML); err != nil {
			return TalkFormat{}, err
		}
		break
	}
	if f.AudioType != "adpcm" {
		return TalkFormat{}, fmt.Errorf("baichuan: talk: camera wants %q audio, which is not implemented", f.AudioType)
	}

	if err := conn.TalkConfig(f); err != nil {
		return TalkFormat{}, err
	}
	m, err := awaitMessage(ctx, conn, MsgIDTalkConfig)
	if err != nil {
		return TalkFormat{}, err
	}
	if st := m.Header.Status(); st != 200 {
		return TalkFormat{}, fmt.Errorf("baichuan: talk: camera refused the session with status %d (%s)", st, ExplainStatus(st))
	}
	return f, nil
}
