package bcudp

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// magicEvent frames a cloud event report ("cf31"): the battery, motion and
// recording notifications a camera sends to its cloud server as it wakes,
// records and sleeps. It uses the same keystream and checksum as discovery,
// keyed on the per-message sequence number carried in the header.
const magicEvent uint32 = 0x2a87cf31

// eventHeaderLen is magic,size,seq,a,b (20 bytes) + uid (32) + crc (4).
const eventHeaderLen = 56

// Event is one decrypted cloud event report. Body is the JSON the camera sent,
// for example {"event":3} for motion. Seq is the sequence number, which is
// also the key the body was encrypted under.
type Event struct {
	Seq  uint32
	UID  string
	Body []byte
}

// DecodeEvent parses and decrypts a cf31 event packet. It verifies the
// checksum against the ciphertext exactly as the camera computes it, and
// rejects a truncated or corrupt packet rather than returning half an event.
func DecodeEvent(b []byte) (Event, error) {
	if len(b) < eventHeaderLen || binary.LittleEndian.Uint32(b) != magicEvent {
		return Event{}, errShort
	}
	size := binary.LittleEndian.Uint32(b[4:])
	if uint64(size) > uint64(len(b)-eventHeaderLen) {
		return Event{}, errShort
	}
	enc := b[eventHeaderLen : eventHeaderLen+int(size)]
	if checksum(enc) != binary.LittleEndian.Uint32(b[52:]) {
		return Event{}, errors.New("bcudp: event checksum mismatch")
	}
	return Event{
		Seq:  binary.LittleEndian.Uint32(b[8:]),
		UID:  trimUID(b[20:52]),
		Body: crypt(binary.LittleEndian.Uint32(b[8:]), enc),
	}, nil
}

func trimUID(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
