package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// One timeout is enough: the page's next dials to that camera fail at once
// for silentFor instead of each waiting out its own timeout.
func TestDialRemembersASilentCamera(t *testing.T) {
	dials := 0
	s := &Server{opts: Options{Dial: func(ctx context.Context, cam Camera) (*baichuan.Conn, error) {
		dials++
		if cam.Address != "192.0.2.9" {
			return nil, errors.New("refused")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}}
	cam := Camera{Address: "192.0.2.9"}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.dial(ctx, cam); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first dial: %v", err)
	}
	start := time.Now()
	if _, err := s.dial(context.Background(), cam); !errors.Is(err, errCameraSilent) {
		t.Fatalf("second dial: %v", err)
	}
	if dials != 1 || time.Since(start) > time.Second {
		t.Fatalf("dials=%d took %v; want the second to fail at once", dials, time.Since(start))
	}
	if _, err := s.dial(context.Background(), Camera{Address: "192.0.2.10"}); errors.Is(err, errCameraSilent) {
		t.Fatal("another camera was refused too")
	}
}
