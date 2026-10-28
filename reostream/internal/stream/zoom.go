package stream

import (
	"context"
	"fmt"

	"github.com/VoltTech21/reostream/internal/baichuan"
)

// ZoomInfo reports a camera's zoom lens: its codec and picture size, from the
// first video frame the camera sends for it over Baichuan, falling back to
// the camera's own RTSP server (NativeInfo). An error means the camera has
// no zoom lens reostream can reach. ctx bounds the whole attempt.
func ZoomInfo(ctx context.Context, address, username, password string) (codec string, width, height int, err error) {
	if codec, width, height, err = baichuanZoomInfo(ctx, address, username, password); err == nil {
		return codec, width, height, nil
	}
	if ctx.Err() != nil {
		return "", 0, 0, err
	}
	return NativeInfo(ctx, address, username, password, "zoom")
}

func baichuanZoomInfo(ctx context.Context, address, username, password string) (string, int, int, error) {
	conn, err := baichuan.Dial(ctx, address, baichuan.Options{Username: username, Password: password})
	if err != nil {
		return "", 0, 0, err
	}
	defer conn.Close()
	// Only a camera with a second channel has a zoom lens to ask for. A
	// single-lens camera may never answer a channel 1 request at all, which
	// would hold every Setup probe for the whole timeout.
	if di, err := baichuan.ParseDeviceInfo(conn.DeviceInfo()); err != nil || di.DeviceInfo.ChannelNum < 2 {
		return "", 0, 0, fmt.Errorf("zoom: the camera reports one channel")
	}
	if err := startZoom(conn, "zoom"); err != nil {
		return "", 0, 0, err
	}
	d := baichuan.NewDepacketizer()
	width, height := 0, 0
	for {
		select {
		case <-ctx.Done():
			return "", 0, 0, fmt.Errorf("zoom: no video over Baichuan: %w", ctx.Err())
		case msg, open := <-conn.Messages():
			if !open {
				return "", 0, 0, fmt.Errorf("zoom: camera closed the connection")
			}
			if msg.Header.MsgID != baichuan.MsgIDVideo || byte(msg.Header.EncOffset) != zoomChannel || len(msg.Payload) == 0 {
				continue
			}
			d.Write(msg.Payload, msg.StartsPacket)
			for {
				f, ok := d.Next()
				if !ok {
					break
				}
				switch f.Kind {
				case baichuan.FrameInfo:
					width, height = f.Width, f.Height
				case baichuan.FrameIFrame, baichuan.FramePFrame:
					return f.Codec, width, height, nil
				}
			}
		}
	}
}
