//go:build !linux

package bcevent

import (
	"context"
	"errors"
	"net/netip"

	"github.com/VoltTech21/reostream/internal/bcudp"
)

// Packet is one captured, decoded cloud event with its UDP endpoints.
type Packet struct {
	Src, Dst netip.AddrPort
	Event    bcudp.Event
}

// Sniffer is unavailable off Linux: passive capture uses AF_PACKET.
type Sniffer struct {
	Iface   string
	Promisc bool
}

// Run always fails here; event sniffing requires Linux AF_PACKET.
func (s *Sniffer) Run(ctx context.Context, fn func(Packet)) error {
	return errors.New("bcevent: packet sniffing is only supported on linux")
}
