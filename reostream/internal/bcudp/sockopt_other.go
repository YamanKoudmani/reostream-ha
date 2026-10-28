//go:build !unix

package bcudp

import "syscall"

func allowBroadcast(network, address string, rc syscall.RawConn) error { return nil }
