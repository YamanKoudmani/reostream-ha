package baichuan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/fakecam"
)

const testTalkAbilityXML = xmlHeader + `<body>
<TalkAbility version="1.1">
<duplexList><duplex>FDX</duplex></duplexList>
<audioStreamModeList><audioStreamMode>followVideoStream</audioStreamMode></audioStreamModeList>
<audioConfigList><audioConfig>
<priority>0</priority><audioType>adpcm</audioType><sampleRate>16000</sampleRate>
<samplePrecision>16</samplePrecision><lengthPerEncoder>1024</lengthPerEncoder><soundTrack>mono</soundTrack>
</audioConfig></audioConfigList>
</TalkAbility>
</body>`

// sentIDs closes conn, waits for the fake camera to see it go, and returns
// the message ids the client sent, in order.
func sentIDs(t *testing.T, cam *fakecam.Camera, conn *Conn) []uint32 {
	t.Helper()
	conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for cam.Closes() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	var ids []uint32
	walkMessages(cam.Received(), func(h Header, _ []byte) bool {
		ids = append(ids, h.MsgID)
		return true
	})
	return ids
}

func countID(ids []uint32, id uint32) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}

func TestOpenTalkNegotiatesTheCamerasFormat(t *testing.T) {
	const nonce = "talk-test-nonce-00000000000"
	cam := fakecam.New(t, synthesizeSessionFixture(t, nonce, "", testDeviceInfoXML,
		fakeReply{id: ConfigMessages["support"], body: string(supportXML("1", "0"))},
		fakeReply{id: MsgIDTalkAbility, body: testTalkAbilityXML},
		fakeReply{id: MsgIDTalkConfig, status: 200}))

	conn, err := Dial(context.Background(), cam.Addr(), Options{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f, err := OpenTalk(ctx, conn)
	if err != nil {
		t.Fatalf("OpenTalk: %v", err)
	}
	if f.AudioType != "adpcm" || f.SampleRate != 16000 || f.Duplex != "FDX" {
		t.Errorf("format = %+v", f)
	}
	if err := conn.Talk(TalkPacket(make([]byte, 516))); err != nil {
		t.Fatalf("talk: %v", err)
	}
	ids := sentIDs(t, cam, conn)
	if countID(ids, MsgIDTalkConfig) != 1 || countID(ids, MsgIDTalk) != 1 {
		t.Errorf("sent %v, want one talk config and one talk packet", ids)
	}
}

// The check that matters most: a camera with no speaker must never be sent
// a talk configuration, since accepting one breaks talk on it until reboot.
func TestOpenTalkNeverConfiguresASpeakerlessCamera(t *testing.T) {
	const nonce = "talk-test-nonce-00000000000"
	cam := fakecam.New(t, synthesizeSessionFixture(t, nonce, "", testDeviceInfoXML,
		fakeReply{id: ConfigMessages["support"], body: string(supportXML("0", "0"))},
		fakeReply{id: MsgIDTalkAbility, body: testTalkAbilityXML},
		fakeReply{id: MsgIDTalkConfig, status: 200}))

	conn, err := Dial(context.Background(), cam.Addr(), Options{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := OpenTalk(ctx, conn); !errors.Is(err, ErrNoSpeaker) {
		t.Fatalf("OpenTalk = %v, want ErrNoSpeaker", err)
	}
	if ids := sentIDs(t, cam, conn); countID(ids, MsgIDTalkConfig) != 0 || countID(ids, MsgIDTalkAbility) != 0 {
		t.Errorf("sent %v to a camera with no speaker", ids)
	}
}

func TestOpenTalkReportsARefusal(t *testing.T) {
	const nonce = "talk-test-nonce-00000000000"
	cam := fakecam.New(t, synthesizeSessionFixture(t, nonce, "", testDeviceInfoXML,
		fakeReply{id: ConfigMessages["support"], body: string(supportXML("1", "0"))},
		fakeReply{id: MsgIDTalkAbility, body: testTalkAbilityXML},
		fakeReply{id: MsgIDTalkConfig, status: 422}))

	conn, err := Dial(context.Background(), cam.Addr(), Options{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := OpenTalk(ctx, conn); err == nil {
		t.Fatal("OpenTalk accepted a 422")
	}
}
