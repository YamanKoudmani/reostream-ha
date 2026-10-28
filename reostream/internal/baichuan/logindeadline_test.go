package baichuan

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// silentCamera accepts connections and never answers a byte: what a camera
// that holds a connection it will not serve looks like from here.
func silentCamera(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				c.Close()
			}
		}()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
		}
	}()
	return ln.Addr().String()
}

// A stream's ctx has no deadline. Canceling it has to end a login the
// camera never answers, or stopping that stream (removing the camera)
// waits forever.
func TestDialReturnsWhenCtxIsCanceledDuringASilentLogin(t *testing.T) {
	addr := silentCamera(t)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := Dial(ctx, addr, Options{Username: "admin", Password: "x"})
		errc <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Dial: got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Dial still blocked in login 3s after its ctx was canceled")
	}
}

// With no deadline and no cancel, a silent login still ends, so the
// supervisor logs it and retries instead of parking the stream for good.
func TestDialTimesOutASilentLoginWithoutACallerDeadline(t *testing.T) {
	old := loginTimeout
	loginTimeout = 200 * time.Millisecond
	t.Cleanup(func() { loginTimeout = old })

	addr := silentCamera(t)
	start := time.Now()
	_, err := Dial(context.Background(), addr, Options{Username: "admin", Password: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial: got %v, want context.DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Dial took %v", d)
	}
}
