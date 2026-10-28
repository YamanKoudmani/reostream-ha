package mqtt

import (
	"bufio"
	"bytes"
	"testing"
)

func TestEncodeRemainingRoundTrips(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 16383, 16384, 2097151} {
		enc := encodeRemaining(n)
		got, err := readRemaining(bufio.NewReader(bytes.NewReader(enc)))
		if err != nil || got != n {
			t.Errorf("remaining %d: got %d err %v", n, got, err)
		}
	}
}

func TestEncodeConnectCarriesCredentials(t *testing.T) {
	b := encodeConnect("reoevent", "user", "secret")
	if b[0] != 0x10 {
		t.Fatalf("CONNECT type byte = %#x", b[0])
	}
	// protocol name, level, flags (username|password|clean = 0xc2), keepalive 0
	if !bytes.Contains(b, []byte("MQTT")) || !bytes.Contains(b, []byte{0x04, 0xc2, 0x00, 0x00}) {
		t.Errorf("CONNECT variable header wrong: % x", b)
	}
	if !bytes.Contains(b, []byte("reoevent")) || !bytes.Contains(b, []byte("user")) || !bytes.Contains(b, []byte("secret")) {
		t.Errorf("CONNECT payload missing fields: % x", b)
	}
}

func TestEncodePublishQoS(t *testing.T) {
	q0 := encodePublish("a/b", []byte("hi"), 0, true, 0)
	if q0[0] != 0x31 { // PUBLISH | retain
		t.Errorf("QoS0 retain header = %#x", q0[0])
	}
	q1 := encodePublish("a/b", []byte("hi"), 1, false, 0x0102)
	if q1[0] != 0x32 { // PUBLISH | qos1
		t.Errorf("QoS1 header = %#x", q1[0])
	}
	if !bytes.Contains(q1, []byte{0x01, 0x02}) {
		t.Errorf("QoS1 packet id missing: % x", q1)
	}
	// QoS0 must not carry a packet id; the body is topic + payload only.
	if !bytes.HasSuffix(q0, []byte("hi")) || !bytes.Contains(q0, []byte("a/b")) {
		t.Errorf("QoS0 body wrong: % x", q0)
	}
}
