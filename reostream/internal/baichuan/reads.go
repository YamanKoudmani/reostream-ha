package baichuan

import (
	"context"
	"fmt"
	"strings"
)

// StatusNotImplemented is the status a camera answers for a config message
// it does not implement, instead of failing the connection. docs/control.md
// establishes this is what makes probing every known read safe.
const StatusNotImplemented = 405

// ReadOutcome is what a config read's answer says about whether a camera
// implements that message.
type ReadOutcome string

const (
	ReadSupported   ReadOutcome = "supported"
	ReadWantsParams ReadOutcome = "wants parameters"
	ReadAbsent      ReadOutcome = "not implemented"
)

// ClassifyRead sorts a config read's reply into the three answers a camera
// can give about a message it was asked for, by status alone.
//
// Status alone, not a non-empty body, is what a camera says about a
// message it does not implement (StatusNotImplemented) versus one it
// understood but refused for lacking arguments (StatusBadRequest): a
// camera answering a refusal is free to send an error document rather than
// an empty one, and a caller that treats "got some bytes back" as "this is
// supported" gets it backwards on exactly that reply. That is not
// hypothetical: cmd/reocam's own probe command checked body length before
// status, and camctl's page checked status alone, and against a real
// RLC-810A they disagreed on three of 103 reads. usercfg answers 400 with
// a body, which the body-first check misread as supported; dns and
// syscpuload answer 200 with an empty body, which it misread as absent
// (and mislabeled "not implemented (405)" without the status ever being
// 405). The status-only rule is correct on both counts, so both callers
// now share it.
func ClassifyRead(status int16) ReadOutcome {
	switch status {
	case StatusNotImplemented:
		return ReadAbsent
	case StatusBadRequest:
		return ReadWantsParams
	default:
		return ReadSupported
	}
}

// requestConfig sends a config read that takes only a channel and waits for
// its reply, or for ctx to end.
//
// This is cmd/reocam's fetch, lifted here so the CLI and anything else in
// this module share one wait loop instead of each keeping its own copy that
// can drift: cmd/reocam's own get, support and probe commands all called an
// identical unexported fetch before this existed. It now delegates to
// ReadConfig (rw.go) rather than keeping its own copy of the same loop.
func requestConfig(ctx context.Context, conn *Conn, id uint32) ([]byte, int16, error) {
	return ReadConfig(ctx, conn, id)
}

// GetSupport reads the camera's own hardware description, message 199 ("get
// support"). This is the read cmd/reocam's support command has always used,
// lifted here rather than duplicated so the CLI and anything else in this
// module cannot answer the question two different ways.
func GetSupport(ctx context.Context, conn *Conn) (Support, error) {
	if err := conn.GetConfig(ConfigMessages["support"]); err != nil {
		return Support{}, err
	}
	m, err := awaitMessage(ctx, conn, ConfigMessages["support"])
	if err != nil {
		return Support{}, err
	}
	status := m.Header.Status()
	if len(m.XML) == 0 && len(m.Payload) == 0 {
		if status == StatusNotImplemented {
			return Support{}, fmt.Errorf("baichuan: this camera does not answer the support read")
		}
		return Support{}, fmt.Errorf("baichuan: support read answered status %d with no body", status)
	}
	return parseSupportReply(m)
}

// parseSupportReply reads the support document from whichever layout the
// reply turns out to use. Every camera here sends it as the XML section
// alone; an E1 Zoom (IPC_515SD6, v3.0.0.2356) answered with a document cut
// off after three lines, so a payload section is tried as well, on its own
// and joined to the XML section. When none parse, the error describes the
// reply's shape, which is what a report from a camera we do not have needs
// to carry.
func parseSupportReply(m Message) (Support, error) {
	s, err := ParseSupport(m.XML)
	if err == nil {
		return s, nil
	}
	for _, alt := range [][]byte{m.PayloadXML, m.WholeXML} {
		if len(alt) == 0 {
			continue
		}
		if s, altErr := ParseSupport(alt); altErr == nil {
			return s, nil
		}
	}
	return Support{}, fmt.Errorf("%w (status %d, class %#04x, body %d bytes, payload offset %d, payload %d bytes, starts %q)",
		err, m.Header.Status(), m.Header.Class, m.Header.MsgLen, m.Header.PayloadOff, len(m.Payload), head(m.XML, 120))
}

// head returns up to n bytes of b with newlines flattened, for an error.
func head(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// GetAbilities reads what the logged in user may do, module by module
// (message 151, AbilityInfo). This is the read cmd/reocam's abilities
// command has always used.
func GetAbilities(ctx context.Context, conn *Conn) ([]Ability, error) {
	if err := conn.Abilities(); err != nil {
		return nil, err
	}
	for {
		select {
		case m, ok := <-conn.Messages():
			if !ok {
				if err := conn.Err(); err != nil {
					return nil, fmt.Errorf("baichuan: connection closed waiting for an ability reply: %w", err)
				}
				return nil, fmt.Errorf("baichuan: connection closed waiting for an ability reply")
			}
			if m.Header.MsgID != MsgIDAbilityInfo || len(m.XML) == 0 {
				continue
			}
			return ParseAbilities(m.XML)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
