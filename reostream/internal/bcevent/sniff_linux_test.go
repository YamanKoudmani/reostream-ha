//go:build linux

package bcevent

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"testing"
)

// cf31 motion packet (UDP payload), from a real Argus PT Ultra.
const motionPayload = "31cf872a0b000000f2020000ffffffff00000000393532373030304e3033314e5253334b0000000000000000000000000000000073736e80461d48691aec187807026a"

// frameAround wraps a UDP payload in Ethernet/IP/UDP headers the way the
// capture path sees them, so parseFrame can be exercised without a NIC.
func frameAround(t *testing.T, payload []byte, src, dst string, sport, dport uint16) []byte {
	t.Helper()
	f := make([]byte, 14+20+8+len(payload))
	binary.BigEndian.PutUint16(f[12:], 0x0800)
	f[14] = 0x45 // IPv4, IHL 5
	f[14+9] = 17 // UDP
	copy(f[26:30], netip.MustParseAddr(src).AsSlice())
	copy(f[30:34], netip.MustParseAddr(dst).AsSlice())
	binary.BigEndian.PutUint16(f[34:], sport)
	binary.BigEndian.PutUint16(f[36:], dport)
	copy(f[42:], payload)
	return f
}

func TestParseFrameExtractsEventAndEndpoints(t *testing.T) {
	payload, err := hex.DecodeString(motionPayload)
	if err != nil {
		t.Fatal(err)
	}
	f := frameAround(t, payload, "192.0.2.10", "203.0.113.150", 61224, 57850)
	p, ok := parseFrame(f)
	if !ok {
		t.Fatal("parseFrame rejected a valid frame")
	}
	if p.Src.String() != "192.0.2.10:61224" || p.Dst.String() != "203.0.113.150:57850" {
		t.Errorf("endpoints = %s -> %s", p.Src, p.Dst)
	}
	if string(p.Event.Body) != `{"event":3}` {
		t.Errorf("body = %q", p.Event.Body)
	}

	// A TCP frame (wrong proto) and a too-short frame must be rejected.
	f[14+9] = 6
	if _, ok := parseFrame(f); ok {
		t.Error("a non-UDP frame should be rejected")
	}
	if _, ok := parseFrame(f[:30]); ok {
		t.Error("a short frame should be rejected")
	}
}
