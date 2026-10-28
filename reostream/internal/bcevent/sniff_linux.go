//go:build linux

package bcevent

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"

	"github.com/VoltTech21/reostream/internal/bcudp"
)

// Packet is one captured, decoded cloud event with the UDP endpoints it was
// sent between.
type Packet struct {
	Src, Dst netip.AddrPort
	Event    bcudp.Event
}

// Sniffer passively reads a battery camera's cloud event reports off a network
// interface. It must run where the camera's traffic to its cloud server is
// visible — the gateway or bridge the camera sits behind, or a mirror of that
// port — and needs CAP_NET_RAW (typically root). It never transmits and never
// touches the camera.
type Sniffer struct {
	// Iface is the interface to capture on, e.g. "enp1s0" or "br-lan".
	Iface string
	// Promisc puts the interface in promiscuous mode. Needed when the
	// camera's frames are bridged past (not addressed to) this host, which
	// is the usual case on a router or bridge. Default when zero value is
	// used via NewSniffer.
	Promisc bool
	// NoFilter skips the kernel BPF and filters in userspace instead. For
	// diagnostics only; the kernel filter is far cheaper on a busy link.
	NoFilter bool
	// Debug logs a count of raw frames seen, to tell "no packets arrive"
	// from "packets arrive but do not decode".
	Debug bool
}

// Run captures until ctx is canceled, calling fn for every valid cf31 event.
// fn runs on the capture goroutine, so it must not block for long; hand work
// to a channel if it might.
func (s *Sniffer) Run(ctx context.Context, fn func(Packet)) error {
	if s.Iface == "" {
		return errors.New("bcevent: no capture interface")
	}
	ifi, err := net.InterfaceByName(s.Iface)
	if err != nil {
		return fmt.Errorf("bcevent: interface %q: %w", s.Iface, err)
	}

	// Capture ETH_P_ALL, not ETH_P_IP: on a bridge member interface a
	// protocol-specific tap does not reliably see bridged frames, which is
	// exactly the camera's traffic here. tcpdump captures ETH_P_ALL for the
	// same reason. The BPF (or the userspace parse) narrows it back to IPv4.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return fmt.Errorf("bcevent: raw socket (need CAP_NET_RAW/root): %w", err)
	}
	defer unix.Close(fd)

	if !s.NoFilter {
		if err := attachFilter(fd); err != nil {
			return fmt.Errorf("bcevent: attach filter: %w", err)
		}
	}
	if err := unix.Bind(fd, &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  ifi.Index,
	}); err != nil {
		return fmt.Errorf("bcevent: bind %s: %w", s.Iface, err)
	}
	if s.Promisc {
		mreq := &unix.PacketMreq{Ifindex: int32(ifi.Index), Type: unix.PACKET_MR_PROMISC}
		if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, mreq); err != nil {
			return fmt.Errorf("bcevent: promisc: %w", err)
		}
	}
	// A 1s receive timeout lets the read loop notice a canceled context
	// without another goroutine racing to close the fd.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1})

	buf := make([]byte, 65536)
	var raw uint64
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("bcevent: recv: %w", err)
		}
		raw++
		if s.Debug && raw%16 == 1 {
			log.Printf("bcevent: %d raw frames seen (last %d bytes)", raw, n)
		}
		if p, ok := parseFrame(buf[:n]); ok {
			fn(p)
		}
	}
}

// parseFrame pulls a cf31 event out of an Ethernet frame. It tolerates IP
// options (the kernel filter requires none, but a userspace caller may pass
// anything), and returns ok=false for anything that is not a decodable event.
func parseFrame(f []byte) (Packet, bool) {
	if len(f) < 14+20+8 || binary.BigEndian.Uint16(f[12:]) != 0x0800 {
		return Packet{}, false
	}
	ihl := int(f[14]&0x0f) * 4
	if ihl < 20 || len(f) < 14+ihl+8 {
		return Packet{}, false
	}
	if f[14+9] != 17 { // not UDP
		return Packet{}, false
	}
	srcIP, _ := netip.AddrFromSlice(f[26:30])
	dstIP, _ := netip.AddrFromSlice(f[30:34])
	udp := f[14+ihl:]
	sport := binary.BigEndian.Uint16(udp[0:])
	dport := binary.BigEndian.Uint16(udp[2:])
	ev, err := bcudp.DecodeEvent(udp[8:])
	if err != nil {
		return Packet{}, false
	}
	return Packet{
		Src:   netip.AddrPortFrom(srcIP, sport),
		Dst:   netip.AddrPortFrom(dstIP, dport),
		Event: ev,
	}, true
}

func htons(v uint16) uint16 { return v<<8 | v>>8 }

// attachFilter loads a classic BPF that passes only IPv4/UDP packets with no
// IP options whose UDP payload begins with the cf31 magic (0x2a87cf31 little
// endian on the wire, read big endian here as 0x31cf872a). Everything else is
// dropped in the kernel, so a busy bridge does not wake userspace per packet.
func attachFilter(fd int) error {
	const (
		ethType  = 12 // u16 ethertype
		ipProto  = 23 // u8 protocol
		ihlByte  = 14 // u8 version/ihl
		magicOff = 42 // 14 eth + 20 ip + 8 udp
		drop     = 10 // index of the "drop" instruction below
	)
	raw, err := bpf.Assemble([]bpf.Instruction{
		bpf.LoadAbsolute{Off: ethType, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: 0x0800, SkipTrue: drop - 2},
		bpf.LoadAbsolute{Off: ipProto, Size: 1},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: 17, SkipTrue: drop - 4},
		bpf.LoadAbsolute{Off: ihlByte, Size: 1},
		bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: 0x0f},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: 5, SkipTrue: drop - 7},
		bpf.LoadAbsolute{Off: magicOff, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpNotEqual, Val: 0x31cf872a, SkipTrue: drop - 9},
		bpf.RetConstant{Val: 0xffff}, // accept
		bpf.RetConstant{Val: 0},      // drop
	})
	if err != nil {
		return err
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, ins := range raw {
		filter[i] = unix.SockFilter{Code: ins.Op, Jt: ins.Jt, Jf: ins.Jf, K: ins.K}
	}
	return unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	})
}
