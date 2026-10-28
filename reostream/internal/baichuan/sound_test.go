package baichuan

import (
	"context"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/fakecam"
)

// The upload is a header on 261, the file in 2048-byte chunks on 261 under
// one message number, then 262, as the Reolink app sends it.
func TestUploadSoundSendsHeaderChunksAndSave(t *testing.T) {
	const nonce = "probe-test-nonce-0000000000"
	replies := []statusReply{{id: MsgIDImportAudio, status: 200}}
	for i := 0; i < 3; i++ {
		replies = append(replies, statusReply{id: MsgIDImportAudio, status: 200})
	}
	replies = append(replies, statusReply{id: MsgIDSaveAudio, status: 200})
	cam := fakecam.New(t, synthesizeSessionFixtureWithStatus(t, nonce, "", testDeviceInfoXML, replies...))
	conn, err := Dial(context.Background(), cam.Addr(), Options{Password: ""})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := UploadSound(ctx, conn, make([]byte, 5000)); err != nil {
		t.Fatalf("UploadSound: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	var ids []uint32
	var sizes []uint32
	var nums = map[uint32]bool{}
	walkMessages(cam.Received(), func(h Header, _ []byte) bool {
		if h.MsgID == MsgIDImportAudio || h.MsgID == MsgIDSaveAudio {
			ids = append(ids, h.MsgID)
			sizes = append(sizes, h.MsgLen-h.PayloadOff)
			if h.MsgID == MsgIDImportAudio {
				nums[h.EncOffset] = true
			}
		}
		return true
	})
	want := []uint32{261, 261, 261, 261, 262}
	if len(ids) != len(want) {
		t.Fatalf("sent %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("sent %v, want %v", ids, want)
		}
	}
	if sizes[1] != 2048 || sizes[2] != 2048 || sizes[3] != 904 {
		t.Errorf("chunk sizes %v, want 2048 2048 904", sizes[1:4])
	}
	if len(nums) != 1 {
		t.Errorf("the header and chunks used %d message numbers, want one", len(nums))
	}
}
