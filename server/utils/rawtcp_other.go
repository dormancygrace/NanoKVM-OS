//go:build !linux

package utils

import "net"

// RawListener returns the listener unchanged off Linux.
func RawListener(l net.Listener) net.Listener { return l }
