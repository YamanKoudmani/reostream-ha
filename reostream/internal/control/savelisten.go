package control

import (
	"fmt"
	"os"
	"strings"

	"github.com/VoltTech21/reostream/internal/config"
)

// SaveListenFlags writes the listen addresses given on the command line
// into an existing config file, for -save-listen (#9). It is opt-in so a
// one-off flag never quietly rewrites a saved config: without -save-listen
// the flags override the file for that run only, as they always have.
//
// Only the listen lines change; everything else in the file stays as
// written. An empty address means that flag was not given and its line is
// left alone. RTSP gets an [rtsp] section if the file has none, since
// -rtsp-listen turns RTSP on anyway; a [control] section is never created,
// because one without a password would leave the page unclaimed.
//
// It reports which lines changed, so the caller can log them. A file that
// would not load afterwards is put back as it was.
func SaveListenFlags(path, listen, rtspListen, controlListen string) ([]string, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(old)
	var changed []string
	set := func(section, value, what string) {
		if value == "" {
			return
		}
		next, ok := setListen(text, section, value)
		if !ok && section == "rtsp" {
			next = upsertSection(text, "rtsp", fmt.Sprintf("[rtsp]\nlisten = %q\n", value))
			ok = true
		}
		if ok && next != text {
			text = next
			changed = append(changed, fmt.Sprintf("%s = %q", what, value))
		}
	}
	set("", listen, "listen")
	set("rtsp", rtspListen, "[rtsp] listen")
	set("control", controlListen, "[control] listen")
	if len(changed) == 0 {
		return nil, nil
	}
	if err := saveConfig(path, text, nil); err != nil {
		return nil, err
	}
	if _, err := config.LoadRaw(path); err != nil {
		_ = saveConfig(path, string(old), nil)
		return nil, fmt.Errorf("the updated config did not load, so it was put back: %w", err)
	}
	return changed, nil
}

// setListen sets the listen line in one section of the config text ("" is
// the top level, before the first table), adding the line under the
// section's header when the section has none. It reports false when the
// section does not exist.
func setListen(text, section, value string) (string, bool) {
	lines := strings.Split(text, "\n")
	line := fmt.Sprintf("listen = %q", value)
	in, header := section == "", -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			if in && (section == "" || header >= 0) {
				break
			}
			if t == "["+section+"]" {
				in, header = true, i
			}
			continue
		}
		if !in {
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == "listen" {
			lines[i] = line
			return strings.Join(lines, "\n"), true
		}
	}
	if section == "" {
		return line + "\n" + text, true
	}
	if header < 0 {
		return text, false
	}
	lines = append(lines[:header+1], append([]string{line}, lines[header+1:]...)...)
	return strings.Join(lines, "\n"), true
}
