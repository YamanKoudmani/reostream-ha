package baichuan

import (
	"context"
	"net"
	"testing"
	"time"
)

// A battery camera's message 234 is answered in kind and kept off Messages.
func TestKeepAliveIsAnswered(t *testing.T) {
	client, cam := net.Pipe()
	defer cam.Close()

	sent := make(chan Header, 16)
	go func() {
		r := NewReader(cam)
		for {
			m, err := r.Next()
			if err != nil {
				close(sent)
				return
			}
			sent <- m.Header
		}
	}()
	go func() {
		const nonce, password = "0123456789abcdef", "pw"
		b := synthesizeSessionFixture(t, nonce, password, xmlHeader+`<body><DeviceInfo/></body>`)
		const off = 0x01020304
		b = append(b, buildMessage(Header{MsgID: MsgIDKeepAlive, Class: ClassZero, EncOffset: off, EncByte: 0xc8}, nil)...)
		b = append(b, buildMessage(Header{MsgID: MsgIDPing, Class: ClassZero, EncByte: 0xc8}, nil)...)
		cam.Write(b)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := newConn(ctx, client, Options{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	select {
	case m := <-c.Messages():
		if m.Header.MsgID != MsgIDPing {
			t.Fatalf("first message on Messages is %d, want the keepalive held back", m.Header.MsgID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing on Messages")
	}

	timeout := time.After(3 * time.Second)
	for {
		select {
		case h, ok := <-sent:
			if !ok {
				t.Fatal("no keepalive reply")
			}
			if h.MsgID != MsgIDKeepAlive {
				continue
			}
			if h.Class != ClassModern24 || h.EncOffset != 0x01020304 || h.Status() != 200 || h.MsgLen != 0 {
				t.Fatalf("reply %+v", h)
			}
			return
		case <-timeout:
			t.Fatal("no keepalive reply")
		}
	}
}
