//go:build !linux

package utils

import (
	"errors"
	"net"
)

func ingressIndex(*net.TCPConn) (int, error) {
	return 0, errors.New("the ingress interface is known only on Linux")
}
