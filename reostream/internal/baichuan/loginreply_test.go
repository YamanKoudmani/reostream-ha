package baichuan

import (
	"bytes"
	"testing"
)

// A Home Hub seals its login reply with the session AES key rather than the
// old cipher. Read the old way it is noise, so it has to be recovered.
func TestLoginReplySealedWithAESIsRecovered(t *testing.T) {
	info := []byte(`<?xml version="1.0" encoding="UTF-8" ?><body><DeviceInfo version="1.1"><type>wifi_hub</type></DeviceInfo></body>`)
	key := AESKey("0123456789ABCDEF", "secret")
	sealed, err := AESEncrypt(key, info)
	if err != nil {
		t.Fatal(err)
	}
	const offset = 0x1234
	// What the reader hands back: the AES bytes run through the old cipher.
	reply := Message{Header: Header{EncOffset: offset}, XML: BCCrypt(offset, sealed)}

	if got := loginReplyXML(reply, key); !bytes.Equal(got, info) {
		t.Errorf("recovered %q", got)
	}
}

func TestLoginReplySealedTheOldWayIsLeftAlone(t *testing.T) {
	info := []byte(`<?xml version="1.0" ?><body><DeviceInfo/></body>`)
	reply := Message{XML: info}
	if got := loginReplyXML(reply, AESKey("n", "p")); !bytes.Equal(got, info) {
		t.Errorf("changed a readable reply to %q", got)
	}
}
