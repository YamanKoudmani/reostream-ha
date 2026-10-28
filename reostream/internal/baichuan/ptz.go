package baichuan

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
)

// PtzControl moves the camera: command is left, right, up, down or stop.
// The document is the one the Reolink desktop client sends (captured
// 2026-10-22: speed 34 for a move, speed 0 with stop). A move continues
// until a stop arrives, so a caller that starts one must send the stop.
func (c *Conn) PtzControl(ctx context.Context, command string, speed int) error {
	switch command {
	case "left", "right", "up", "down":
	case "stop":
		speed = 0
	default:
		return fmt.Errorf("baichuan: ptz: unknown command %q", command)
	}
	body := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<PtzControl version=\"1.1\">\n<channelId>%d</channelId>\n<speed>%d</speed>\n<command>%s</command>\n</PtzControl>\n</body>\n",
		c.opts.Channel, speed, command)
	if err := c.SetConfig(MsgIDPtzControl, []byte(body)); err != nil {
		return fmt.Errorf("baichuan: ptz %s: %w", command, err)
	}
	if _, status, err := awaitReply(ctx, c, MsgIDPtzControl); err != nil {
		return fmt.Errorf("baichuan: ptz %s: awaiting ack: %w", command, err)
	} else if status != 0 && status != 200 {
		return fmt.Errorf("baichuan: ptz %s: camera answered status %d", command, status)
	}
	return nil
}

// Preset is one saved camera position.
type Preset struct {
	ID   int    `xml:"id"`
	Name string `xml:"name"`
}

// PtzPresets reads the saved positions (message 190) and how many the camera
// can hold.
func (c *Conn) PtzPresets(ctx context.Context) ([]Preset, int, error) {
	doc, status, err := ReadConfig(ctx, c, MsgIDPtzPreset2)
	if err != nil {
		return nil, 0, fmt.Errorf("baichuan: presets: %w", err)
	}
	if status != 200 && status != 0 {
		return nil, 0, fmt.Errorf("baichuan: presets: camera answered status %d", status)
	}
	var d struct {
		Max  int      `xml:"PtzPreset>maxPresetNum"`
		List []Preset `xml:"PtzPreset>presetList>preset"`
	}
	if err := xml.Unmarshal(doc, &d); err != nil {
		return nil, 0, fmt.Errorf("baichuan: presets: %w", err)
	}
	return d.List, d.Max, nil
}

// PtzPreset saves the current position as preset id (command "setPos") or
// moves to it ("toPos"). The document is the client's (message 19).
func (c *Conn) PtzPreset(ctx context.Context, command string, id int, name string) error {
	if command != "setPos" && command != "toPos" {
		return fmt.Errorf("baichuan: preset: unknown command %q", command)
	}
	var esc strings.Builder
	if err := xml.EscapeText(&esc, []byte(name)); err != nil {
		return err
	}
	body := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<body>\n<PtzPreset version=\"1.1\">\n<channelId>%d</channelId>\n<presetList>\n<preset>\n<id>%d</id>\n<command>%s</command>\n<name>%s</name>\n</preset>\n</presetList>\n</PtzPreset>\n</body>\n",
		c.opts.Channel, id, command, esc.String())
	status, err := WriteConfig(ctx, c, MsgIDPtzPreset, []byte(body))
	if err != nil {
		return fmt.Errorf("baichuan: preset %s: %w", command, err)
	}
	if status != 0 && status != 200 {
		return fmt.Errorf("baichuan: preset %s: camera answered status %d", command, status)
	}
	return nil
}

// PtzGuard is the guard position the camera returns to when left alone:
// whether it does (Enable), after how many seconds (Timeout), and whether a
// guard position has been set at all (Valid). Doc is the camera's own
// document, kept so a write echoes it back with only these fields changed.
type PtzGuard struct {
	Enable  bool
	Timeout int
	Valid   bool
	Doc     []byte
}

// GetPtzGuard reads the guard (message 332).
func (c *Conn) GetPtzGuard(ctx context.Context) (PtzGuard, error) {
	doc, status, err := ReadConfig(ctx, c, MsgIDPtzGuard2)
	if err != nil {
		return PtzGuard{}, fmt.Errorf("baichuan: guard: %w", err)
	}
	if status != 200 && status != 0 {
		return PtzGuard{}, fmt.Errorf("baichuan: guard: camera answered status %d", status)
	}
	return parseGuard(doc)
}

func parseGuard(doc []byte) (PtzGuard, error) {
	var d struct {
		Timeout int `xml:"PtzGuard>timeout"`
		Enable  int `xml:"PtzGuard>benable"`
		Valid   int `xml:"PtzGuard>bvalid"`
	}
	if err := xml.Unmarshal(doc, &d); err != nil {
		return PtzGuard{}, fmt.Errorf("baichuan: guard: %w", err)
	}
	return PtzGuard{Enable: d.Enable == 1, Timeout: d.Timeout, Valid: d.Valid == 1, Doc: doc}, nil
}

// SetPtzGuard writes doc (the camera's own guard document, edited) to the
// guard write (message 331).
func (c *Conn) SetPtzGuard(ctx context.Context, doc []byte) error {
	status, err := WriteConfig(ctx, c, MsgIDPtzGuard, doc)
	if err != nil {
		return fmt.Errorf("baichuan: guard: %w", err)
	}
	if status != 0 && status != 200 {
		return fmt.Errorf("baichuan: guard: camera answered status %d", status)
	}
	return nil
}

// Calibrate is the Reolink client's pan/tilt self test: MsgIDPtzAutoTest2
// (341) starts it, MsgIDPtzAutoTest (340) reads PtzAutoTest/testStat. Both
// carry only the channel. Captured from the client on an Argus PT Ultra:
// 341 sent cold answered 400, sent after a 340 read (as the client does)
// 200, and testStat went from 0 to 2 when it finished.

// PtzCalibrateState reads the self test's state (testStat; 2 when one has
// finished).
func (c *Conn) PtzCalibrateState(ctx context.Context) (int, error) {
	if err := c.RawChannelRequest(MsgIDPtzAutoTest); err != nil {
		return 0, err
	}
	doc, status, err := awaitReply(ctx, c, MsgIDPtzAutoTest)
	if err != nil {
		return 0, err
	}
	if status != 200 {
		return 0, fmt.Errorf("baichuan: calibrate state: camera answered status %d", status)
	}
	var v struct {
		TestStat int `xml:"PtzAutoTest>testStat"`
	}
	if err := xml.Unmarshal(doc, &v); err != nil {
		return 0, fmt.Errorf("baichuan: calibrate state: %w", err)
	}
	return v.TestStat, nil
}

// PtzCalibrate starts the self test, reading its state first as the client
// does (the camera refuses a cold start).
func (c *Conn) PtzCalibrate(ctx context.Context) error {
	if _, err := c.PtzCalibrateState(ctx); err != nil {
		return err
	}
	if err := c.RawChannelRequest(MsgIDPtzAutoTest2); err != nil {
		return err
	}
	_, status, err := awaitReply(ctx, c, MsgIDPtzAutoTest2)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("baichuan: calibrate: camera answered status %d", status)
	}
	return nil
}
