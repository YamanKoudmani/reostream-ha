package mqtt

import (
	"bufio"
	"net"
	"testing"
	"time"
)

// TestPublishOverTCP drives the client against a minimal in-process broker on
// a real socket: it must send a well-formed CONNECT, wait for CONNACK, send a
// QoS 1 PUBLISH and wait for its PUBACK. This exercises the whole network path
// the feeder uses, without touching any real broker.
func TestPublishOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type pub struct {
		topic   string
		payload string
		qos     byte
		retain  bool
	}
	got := make(chan pub, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)

		// CONNECT -> CONNACK (accepted).
		typ, _, err := readPacket(br)
		if err != nil || typ>>4 != 1 {
			t.Errorf("expected CONNECT, got type %d err %v", typ>>4, err)
			return
		}
		conn.Write([]byte{0x20, 0x02, 0x00, 0x00})

		// PUBLISH -> decode topic, qos, retain, payload, then PUBACK.
		typ, body, err := readPacket(br)
		if err != nil || typ>>4 != 3 {
			t.Errorf("expected PUBLISH, got type %d err %v", typ>>4, err)
			return
		}
		qos := (typ >> 1) & 0x03
		tl := int(body[0])<<8 | int(body[1])
		topic := string(body[2 : 2+tl])
		rest := body[2+tl:]
		var id uint16
		if qos > 0 {
			id = uint16(rest[0])<<8 | uint16(rest[1])
			rest = rest[2:]
		}
		got <- pub{topic: topic, payload: string(rest), qos: qos, retain: typ&0x01 != 0}
		if qos == 1 {
			conn.Write([]byte{0x40, 0x02, byte(id >> 8), byte(id)})
		}
	}()

	c := New(ln.Addr().String(), "reoevent-test", "", "")
	defer c.Close()
	if err := c.Publish("reostream/cam/motion", []byte("ON"), 1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case p := <-got:
		if p.topic != "reostream/cam/motion" || p.payload != "ON" || p.qos != 1 {
			t.Errorf("broker saw %+v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("broker never saw the publish")
	}
}

// TestPublishReconnectsAfterBrokerDrop confirms a dropped connection heals on
// the next publish rather than wedging the client.
func TestPublishReconnectsAfterBrokerDrop(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serve := func(ack bool) {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(conn)
		readPacket(br) // CONNECT
		conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		_, body, _ := readPacket(br) // PUBLISH (QoS 1): id is after the topic
		if ack && len(body) >= 2 {
			tl := int(body[0])<<8 | int(body[1])
			id := body[2+tl : 2+tl+2] // the two packet-id bytes
			conn.Write(append([]byte{0x40, 0x02}, id...))
		}
		conn.Close() // drop after the first publish either way
	}

	c := New(ln.Addr().String(), "reoevent-test", "", "")
	defer c.Close()

	go serve(true)
	if err := c.Publish("t", []byte("1"), 1, false); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	// The broker closed the socket; the next publish must redial and succeed.
	go serve(true)
	if err := c.Publish("t", []byte("2"), 1, false); err != nil {
		t.Fatalf("publish after drop did not reconnect: %v", err)
	}
}
