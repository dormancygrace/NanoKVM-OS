//go:build !linux || (!amd64 && !arm64 && !riscv64)

package udpfast

import (
	"github.com/pion/transport/v5"
	"net"
)

func Wrap(c *net.UDPConn) transport.UDPConn { return c }
