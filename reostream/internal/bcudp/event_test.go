package bcudp

import (
	"encoding/hex"
	"testing"
)

// These are cf31 packets captured from an Argus PT Ultra, its UID replaced on the camera's
// own path to its cloud server: a motion report, a status report and a codec
// report, verbatim off the wire.
const (
	pktMotion = "31cf872a0b000000f2020000ffffffff00000000393532373030303054455354554944310000000000000000000000000000000073736e80461d48691aec187807026a"
	pktStatus = "31cf872a82010000f3020000ffffffff00000000393532373030303054455354554944310000000000000000000000000000000005a752bd451d4869e5ec1878040025146f1218ef7b3f05b7fcc3f49291d043b5aafad06357524865efec09780403260e7d565da07a6e4babae94e49f96c853e7eba2df7858595e7af4a0566b1213765c2c1605a0242d13a4b99ca9d5c28a53b9a9fad065584c4e7ae4a0566e0c0823017b5141b62e311df4ad89a3cb85de40afbab994715256437adfe1032f504535027d4a53f26d3f05b5fac2f089858c1ef0eca685651c051c33a2ea03294a5364416f5c41ae3c7f5ef2e9c0bd87c58753b9b5e5c02e121d4f69a2b8586a0a003b1a390b53b82c2413a4a893b5879dd45da1fba5d02d0d130f71f4e14e600e1d3554221153b82e311df6a88ee49f90dd5da1ebb59735040f5033a2eb1e394b4535027f4a53f27b6f56f6a3d8fc958bc602faeba29b7a5b1d172ab6b65d680d0522146f1118e477425ceea5d8fc9492d75da1efbf947e6152427be5a0566b0c013b1a3a0f17eb416f4cf5a2d8fc8894d65da1f9a6ad63474f483dbab2407850546367200915e73c270faae99baac4d5892eeef9a6d02d0842"
	pktCodec  = "31cf872a49000000f4020000ffffffff00000000393532373030303054455354554944310000000000000000000000000000000056e6428c441d4869e4ec187805093b1a260902f67d6e46a4f6caea87cb8b15e6faf4c8357b731d3fb3b64c341f5f37097e5651b23f2d1fb7fccae69588d451b3e1e4c2270f0f1d3fb1a2597842"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeEventReadsMotionStatusAndCodec(t *testing.T) {
	const uid = "95270000TESTUID1"
	for _, tc := range []struct {
		name string
		pkt  string
		seq  uint32
		body string
	}{
		{"motion", pktMotion, 0x2f2, `{"event":3}`},
		{"codec", pktCodec, 0x2f4, `{"event":8,"hostbsy":0,"codec":"DL0 24 n n 100 0 0 100 0 0 0x200000 0 5"}`},
	} {
		ev, err := DecodeEvent(mustHex(t, tc.pkt))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if ev.Seq != tc.seq {
			t.Errorf("%s: seq = %#x, want %#x", tc.name, ev.Seq, tc.seq)
		}
		if ev.UID != uid {
			t.Errorf("%s: uid = %q, want %q", tc.name, ev.UID, uid)
		}
		if string(ev.Body) != tc.body {
			t.Errorf("%s: body = %q, want %q", tc.name, ev.Body, tc.body)
		}
	}

	// The status report is long; check it decrypts to valid JSON carrying the
	// fields a feeder reads rather than pinning the whole string.
	ev, err := DecodeEvent(mustHex(t, pktStatus))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":12`, `"pct":79`, `"tm":29`, `"wifi_rssi":-32`, `"hostpwr":1`} {
		if !contains(ev.Body, want) {
			t.Errorf("status body missing %q: %s", want, ev.Body)
		}
	}
}

func TestDecodeEventRejectsCorruptAndShort(t *testing.T) {
	if _, err := DecodeEvent([]byte{0x31, 0xcf}); err == nil {
		t.Error("a short packet should fail")
	}
	good := mustHex(t, pktMotion)
	if _, err := DecodeEvent(good); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xff // flip a ciphertext byte: checksum must catch it
	if _, err := DecodeEvent(bad); err == nil {
		t.Error("a corrupt packet should fail the checksum")
	}
	notEvent := append([]byte(nil), good...)
	notEvent[0] = 0x10 // wrong magic
	if _, err := DecodeEvent(notEvent); err == nil {
		t.Error("a non-event magic should fail")
	}
}

func contains(b []byte, s string) bool {
	return len(s) == 0 || indexOf(string(b), s) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
