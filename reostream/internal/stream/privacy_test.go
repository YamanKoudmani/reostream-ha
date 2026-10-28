package stream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
	"github.com/VoltTech21/reostream/internal/fakecam"
	"github.com/VoltTech21/reostream/internal/hub"
)

const privacyXMLHeader = `<?xml version="1.0" encoding="UTF-8" ?>`

func privacyMessage(h baichuan.Header, body []byte) []byte {
	h.MsgLen = uint32(len(body))
	return append(h.Encode(), body...)
}

// privacyCamera logs in and then sends replies, the way an E1 Pro in
// privacy mode answered the Reolink app (reocapture log on issue #13,
// 2026-10-25).
func privacyCamera(t *testing.T, replies ...[]byte) *fakecam.Camera {
	t.Helper()
	const nonce = "privacy-test-nonce-00000000"
	plainNonce := privacyXMLHeader + `<body><Encryption version="1.1"><type>md5</type><nonce>` + nonce + `</nonce></Encryption></body>`
	out := privacyMessage(baichuan.Header{MsgID: baichuan.MsgIDLogin, Class: baichuan.ClassModern20, EncByte: baichuan.NegotiateByte, DirByte: baichuan.DirReply},
		baichuan.BCCrypt(0, []byte(plainNonce)))
	out = append(out, privacyMessage(baichuan.Header{MsgID: baichuan.MsgIDLogin, Class: baichuan.ClassZero},
		baichuan.BCCrypt(0, []byte(privacyXMLHeader+`<body><DeviceInfo><typeInfo>IPC</typeInfo><sleep>1</sleep></DeviceInfo></body>`)))...)
	for _, r := range replies {
		out = append(out, r...)
	}
	return fakecam.New(t, out)
}

func privacyReply(t *testing.T, id uint32, status int16, body string) []byte {
	t.Helper()
	key := baichuan.AESKey("privacy-test-nonce-00000000", "")
	enc, err := baichuan.AESEncrypt(key, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return privacyMessage(baichuan.Header{MsgID: id, Class: baichuan.ClassZero, EncByte: byte(uint16(status) & 0xff), DirByte: byte(uint16(status) >> 8)}, enc)
}

// While privacy mode is on, the camera turns down the preview with 425.
func TestPreviewRefusedWith425IsPrivacyMode(t *testing.T) {
	cam := privacyCamera(t, privacyReply(t, baichuan.MsgIDVideo, 425, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, Config{Name: "e1", Address: cam.Addr(), Stream: "sub"}, hub.New(8))
	if !errors.Is(err, ErrPrivacy) {
		t.Fatalf("Run = %v, want ErrPrivacy", err)
	}
}

// Turning privacy on mid-stream: the camera reports it (623) and stops.
func TestSleepReportEndsTheStreamAsPrivacyMode(t *testing.T) {
	cam := privacyCamera(t, privacyReply(t, 623, 200, privacyXMLHeader+`<body><sleepStatus version="1.1"><sleep>1</sleep></sleepStatus></body>`))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, Config{Name: "e1", Address: cam.Addr(), Stream: "sub"}, hub.New(8))
	if !errors.Is(err, ErrPrivacy) {
		t.Fatalf("Run = %v, want ErrPrivacy", err)
	}
}

// A sleep report saying privacy is OFF is not a reason to stop.
func TestSleepReportOffDoesNotStop(t *testing.T) {
	cam := privacyCamera(t, privacyReply(t, 623, 200, privacyXMLHeader+`<body><sleepStatus version="1.1"><sleep>0</sleep></sleepStatus></body>`))
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	err := Run(ctx, Config{Name: "e1", Address: cam.Addr(), Stream: "sub"}, hub.New(8))
	if errors.Is(err, ErrPrivacy) {
		t.Fatalf("Run = %v; sleep 0 must not read as privacy mode", err)
	}
}
