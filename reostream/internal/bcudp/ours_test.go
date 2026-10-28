package bcudp

import (
	"context"
	"net"
	"testing"
	"time"
)

// A session is ours from the dial to a moment after its close, so the wake
// it causes is never taken for motion.
func TestSessionIsOursUntilShortlyAfterClose(t *testing.T) {
	lo := net.ParseIP("127.0.0.1")
	f := newFakeCam(t)
	c := dialFake(t, f)
	if !Ours(lo, 0) {
		t.Fatal("an open session is not ours")
	}
	c.Close()
	if !Ours(lo, time.Second) {
		t.Fatal("a session closed just now is not ours within the grace")
	}
	time.Sleep(20 * time.Millisecond)
	if Ours(lo, 10*time.Millisecond) {
		t.Fatal("a session closed before the grace is still ours")
	}
}

// A dial the camera never answers still woke it, or tried to.
func TestFailedDialIsOursToo(t *testing.T) {
	lo := net.ParseIP("127.0.0.1")
	f := newFakeCam(t)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if c, err := dial(ctx, "127.0.0.1", "95270000WRONGUID", []int{f.port()}); err == nil {
		c.Close()
		t.Fatal("a wrong UID connected")
	}
	if !Ours(lo, time.Second) {
		t.Fatal("a failed dial is not ours")
	}
	if Ours(net.ParseIP("192.0.2.99"), time.Hour) {
		t.Fatal("a camera never dialled is ours")
	}
}
