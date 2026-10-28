package bcudp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// searchPort is where a battery camera answers C2D_S.
var searchPort = 2015

// Info is a battery camera's answer to a search.
type Info struct {
	Addr    net.IP
	UID     string
	Asleep  bool // the reply carried a battery block, which only a sleeping camera sends
	Battery int  // percent, or -1 when the reply carried none
}

// Search asks the camera at host for its UID and sleep state. It does not
// wake the camera.
func Search(ctx context.Context, host string) (Info, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	ip, err := resolve(ctx, host)
	if err != nil {
		return Info{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	found, err := search(ctx, &net.UDPAddr{IP: ip, Port: searchPort}, true)
	if err != nil {
		return Info{}, err
	}
	if len(found) == 0 {
		return Info{}, fmt.Errorf("bcudp: search %s: no answer: %w", ip, ctx.Err())
	}
	return found[0], nil
}

// Discover broadcasts a search until ctx ends and returns every battery
// camera that answered, one entry per address.
func Discover(ctx context.Context) ([]Info, error) {
	return search(ctx, &net.UDPAddr{IP: net.IPv4bcast, Port: searchPort}, false)
}

func search(ctx context.Context, dst *net.UDPAddr, first bool) ([]Info, error) {
	lc := net.ListenConfig{Control: allowBroadcast}
	pc, err := lc.ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("bcudp: listen: %w", err)
	}
	sock := pc.(*net.UDPConn)
	defer sock.Close()

	port := sock.LocalAddr().(*net.UDPAddr).Port
	pkt := encodeDiscovery(newTID(), searchXML(port))
	var found []Info
	seen := map[string]bool{}
	buf := make([]byte, 65536)
	for {
		_, _ = sock.WriteToUDP(pkt, dst)
		next := time.Now().Add(resendEvery)
		for time.Now().Before(next) {
			if ctx.Err() != nil {
				return found, nil
			}
			dl := next
			if d, ok := ctx.Deadline(); ok && d.Before(dl) {
				dl = d
			}
			_ = sock.SetReadDeadline(dl)
			n, from, err := sock.ReadFromUDP(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return found, fmt.Errorf("bcudp: search: %w", err)
			}
			if !dst.IP.Equal(net.IPv4bcast) && !from.IP.Equal(dst.IP) {
				continue
			}
			_, body, err := decodeDiscovery(buf[:n])
			if err != nil {
				continue
			}
			m, err := parseP2P(body)
			if err != nil || m.SearchR == nil || seen[from.IP.String()] {
				continue
			}
			seen[from.IP.String()] = true
			info := Info{Addr: from.IP, UID: m.SearchR.UID, Battery: -1}
			if m.SearchR.Bat != nil {
				info.Asleep = true
				info.Battery = m.SearchR.Bat.Pct
			}
			found = append(found, info)
			if first {
				return found, nil
			}
		}
	}
}
