package bcudp

import (
	"bytes"
	"testing"
)

func TestChecksum(t *testing.T) {
	if got := checksum([]byte("123456789")); got != 0x2dfd2d88 {
		t.Fatalf("checksum = %#x, want 0x2dfd2d88", got)
	}
}

func TestCryptKeystream(t *testing.T) {
	got := crypt(1, make([]byte, 8))
	want := []byte{0x4c, 0x3c, 0x2d, 0x1f, 0x8e, 0x7f, 0x6c, 0x5a}
	if !bytes.Equal(got, want) {
		t.Fatalf("keystream = %x, want %x", got, want)
	}
}

func TestDiscoveryRoundTrip(t *testing.T) {
	body := connectXML("95270000TESTUID1", 40000, 77, "")
	pkt := encodeDiscovery(529, body)
	if magicOf(pkt) != magicDiscovery {
		t.Fatal("wrong magic")
	}
	if bytes.Contains(pkt, []byte("C2D_C")) {
		t.Fatal("body went out in the clear")
	}
	tid, got, err := decodeDiscovery(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if tid != 529 || !bytes.Equal(got, body) {
		t.Fatalf("tid %d body %q", tid, got)
	}
	m, err := parseP2P(got)
	if err != nil || m.Connect == nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Connect.UID != "95270000TESTUID1" || m.Connect.Port != 40000 || m.Connect.CID != 77 || m.Connect.MTU != 1350 {
		t.Fatalf("parsed %+v", *m.Connect)
	}

	pkt[len(pkt)-1] ^= 1
	if _, _, err := decodeDiscovery(pkt); err == nil {
		t.Fatal("a corrupted packet passed its checksum")
	}
}

func TestSearchReplyParses(t *testing.T) {
	asleep := `<P2P><D2C_S_R><bat><low>0</low><pct>22</pct><c>0</c><p>0</p></bat><uid>95270000TESTUID1</uid></D2C_S_R></P2P>`
	m, err := parseP2P([]byte(asleep))
	if err != nil || m.SearchR == nil || m.SearchR.Bat == nil || m.SearchR.Bat.Pct != 22 || m.SearchR.UID != "95270000TESTUID1" {
		t.Fatalf("asleep reply: %+v %v", m.SearchR, err)
	}
	m, err = parseP2P([]byte("<P2P>\n<D2C_S_R>\n<uid>X</uid>\n</D2C_S_R>\n</P2P>\n\x00"))
	if err != nil || m.SearchR == nil || m.SearchR.Bat != nil {
		t.Fatalf("awake reply: %+v %v", m.SearchR, err)
	}
}

func TestDataAndAckRoundTrip(t *testing.T) {
	d, err := decodeData(encodeData(9, 42, []byte("hello")))
	if err != nil || d.conn != 9 || d.id != 42 || string(d.payload) != "hello" {
		t.Fatalf("data %+v %v", d, err)
	}
	a, err := decodeAck(encodeAck(ackPacket{conn: 9, group: none, last: none, have: []byte{0, 1}}))
	if err != nil || a.conn != 9 || a.group != none || a.last != none || !bytes.Equal(a.have, []byte{0, 1}) {
		t.Fatalf("ack %+v %v", a, err)
	}
	if _, err := decodeData(encodeData(1, 1, []byte("x"))[:20]); err == nil {
		t.Fatal("truncated data packet accepted")
	}
}
