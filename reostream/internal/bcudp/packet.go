// Package bcudp carries a Baichuan byte stream over the UDP transport that
// battery cameras speak instead of TCP port 9000. See docs/udp.md.
package bcudp

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"hash/crc32"
	"strconv"
	"strings"
)

// Packet magics, little endian on the wire.
const (
	magicDiscovery uint32 = 0x2a87cf3a
	magicData      uint32 = 0x2a87cf10
	magicAck       uint32 = 0x2a87cf20
)

const (
	discoveryHeaderLen = 20
	dataHeaderLen      = 20
	ackHeaderLen       = 28

	// maxPacket is the largest packet sent, header included.
	maxPacket  = 1350
	maxPayload = maxPacket - dataHeaderLen

	// none is the id an ack names when nothing has been received yet.
	none uint32 = 0xffffffff
)

var errShort = errors.New("bcudp: short packet")

var cipherTable = [8]uint32{
	0x1f2d3c4b, 0x5a6c7f8d, 0x38172e4b, 0x8271635a,
	0x863f1a2b, 0xa5c6f7d8, 0x8371e1b4, 0x17f2d3a5,
}

// crypt XORs b with the discovery keystream for tid. It is its own inverse.
func crypt(tid uint32, b []byte) []byte {
	var key [32]byte
	for i, w := range cipherTable {
		binary.LittleEndian.PutUint32(key[i*4:], w+tid)
	}
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ key[i%32]
	}
	return out
}

// checksum is the reflected CRC-32 with initial value 0 and no final XOR.
func checksum(b []byte) uint32 {
	return crc32.Update(0xffffffff, crc32.IEEETable, b) ^ 0xffffffff
}

// encodeDiscovery encrypts xmlBody under tid and frames it.
func encodeDiscovery(tid uint32, xmlBody []byte) []byte {
	enc := crypt(tid, xmlBody)
	b := make([]byte, discoveryHeaderLen+len(enc))
	binary.LittleEndian.PutUint32(b[0:], magicDiscovery)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(enc)))
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[12:], tid)
	binary.LittleEndian.PutUint32(b[16:], checksum(enc))
	copy(b[discoveryHeaderLen:], enc)
	return b
}

// decodeDiscovery returns the tid and decrypted XML of a discovery packet.
func decodeDiscovery(b []byte) (uint32, []byte, error) {
	if len(b) < discoveryHeaderLen || binary.LittleEndian.Uint32(b) != magicDiscovery {
		return 0, nil, errShort
	}
	size := binary.LittleEndian.Uint32(b[4:])
	if uint64(size) > uint64(len(b)-discoveryHeaderLen) {
		return 0, nil, errShort
	}
	tid := binary.LittleEndian.Uint32(b[12:])
	enc := b[discoveryHeaderLen : discoveryHeaderLen+int(size)]
	if checksum(enc) != binary.LittleEndian.Uint32(b[16:]) {
		return 0, nil, errors.New("bcudp: discovery checksum mismatch")
	}
	return tid, crypt(tid, enc), nil
}

type dataPacket struct {
	conn    uint32
	id      uint32
	payload []byte
}

func encodeData(conn, id uint32, payload []byte) []byte {
	b := make([]byte, dataHeaderLen+len(payload))
	binary.LittleEndian.PutUint32(b[0:], magicData)
	binary.LittleEndian.PutUint32(b[4:], conn)
	binary.LittleEndian.PutUint32(b[12:], id)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(payload)))
	copy(b[dataHeaderLen:], payload)
	return b
}

func decodeData(b []byte) (dataPacket, error) {
	if len(b) < dataHeaderLen {
		return dataPacket{}, errShort
	}
	size := binary.LittleEndian.Uint32(b[16:])
	if uint64(size) > uint64(len(b)-dataHeaderLen) {
		return dataPacket{}, errShort
	}
	return dataPacket{
		conn:    binary.LittleEndian.Uint32(b[4:]),
		id:      binary.LittleEndian.Uint32(b[12:]),
		payload: b[dataHeaderLen : dataHeaderLen+int(size)],
	}, nil
}

type ackPacket struct {
	conn  uint32
	group uint32
	last  uint32
	have  []byte // one byte per id after last: 1 received, 0 missing
}

func encodeAck(a ackPacket) []byte {
	b := make([]byte, ackHeaderLen+len(a.have))
	binary.LittleEndian.PutUint32(b[0:], magicAck)
	binary.LittleEndian.PutUint32(b[4:], a.conn)
	binary.LittleEndian.PutUint32(b[12:], a.group)
	binary.LittleEndian.PutUint32(b[16:], a.last)
	binary.LittleEndian.PutUint32(b[24:], uint32(len(a.have)))
	copy(b[ackHeaderLen:], a.have)
	return b
}

func decodeAck(b []byte) (ackPacket, error) {
	if len(b) < ackHeaderLen {
		return ackPacket{}, errShort
	}
	size := binary.LittleEndian.Uint32(b[24:])
	if uint64(size) > uint64(len(b)-ackHeaderLen) {
		return ackPacket{}, errShort
	}
	return ackPacket{
		conn:  binary.LittleEndian.Uint32(b[4:]),
		group: binary.LittleEndian.Uint32(b[12:]),
		last:  binary.LittleEndian.Uint32(b[16:]),
		have:  b[ackHeaderLen : ackHeaderLen+int(size)],
	}, nil
}

func magicOf(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

// p2p is every discovery message either side sends.
type p2p struct {
	XMLName  xml.Name     `xml:"P2P"`
	Search   *searchReq   `xml:"C2D_S"`
	SearchR  *searchReply `xml:"D2C_S_R"`
	Connect  *connectReq  `xml:"C2D_C"`
	ConnectR *connectRep  `xml:"D2C_C_R"`
	CHB      *session     `xml:"C2D_HB"`
	DHB      *session     `xml:"D2C_HB"`
	CDisc    *session     `xml:"C2D_DISC"`
	DDisc    *session     `xml:"D2C_DISC"`
}

type searchReq struct {
	Port int `xml:"to>port"`
}

type battery struct {
	Low int `xml:"low"`
	Pct int `xml:"pct"`
	C   int `xml:"c"`
	P   int `xml:"p"`
}

type searchReply struct {
	Bat *battery `xml:"bat"`
	UID string   `xml:"uid"`
}

type connectReq struct {
	UID   string `xml:"uid"`
	Port  int    `xml:"cli>port"`
	CID   uint32 `xml:"cid"`
	MTU   int    `xml:"mtu"`
	Debug int    `xml:"debug"`
	P     string `xml:"p"`
}

type connectRep struct {
	Rsp int    `xml:"rsp"`
	CID uint32 `xml:"cid"`
	DID uint32 `xml:"did"`
}

type session struct {
	CID uint32 `xml:"cid"`
	DID uint32 `xml:"did"`
}

func parseP2P(b []byte) (p2p, error) {
	var m p2p
	err := xml.Unmarshal([]byte(strings.TrimRight(string(b), "\x00")), &m)
	return m, err
}

// render writes a message the way the camera does, a newline after every tag.
func render(name string, fields ...string) []byte {
	var b strings.Builder
	b.WriteString("<P2P>\n<" + name + ">\n")
	for _, f := range fields {
		b.WriteString(f)
	}
	b.WriteString("</" + name + ">\n</P2P>\n")
	return []byte(b.String())
}

func tag(name string, v any) string {
	var s string
	switch x := v.(type) {
	case string:
		var sb strings.Builder
		_ = xml.EscapeText(&sb, []byte(x))
		s = sb.String()
	case int:
		s = strconv.Itoa(x)
	case uint32:
		s = strconv.FormatUint(uint64(x), 10)
	}
	return "<" + name + ">" + s + "</" + name + ">\n"
}

func searchXML(port int) []byte {
	return render("C2D_S", "<to>\n", tag("port", port), "</to>\n")
}

func connectXML(uid string, port int, cid uint32, mac string) []byte {
	return render("C2D_C",
		tag("uid", uid),
		"<cli>\n", tag("port", port), "</cli>\n",
		tag("cid", cid),
		tag("mtu", maxPacket),
		tag("debug", 0),
		tag("p", mac))
}

func sessionXML(name string, cid, did uint32) []byte {
	return render(name, tag("cid", cid), tag("did", did))
}
