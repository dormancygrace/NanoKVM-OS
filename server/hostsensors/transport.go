package hostsensors

import (
	"bytes"
	"net"
)

// SplitLines adds data to the pending line and hands out the lines
// that are complete; it returns the new pending line and whether the line in
// progress is being dropped for its length.
func SplitLines(line []byte, overflow bool, data []byte, accept func([]byte)) ([]byte, bool) {
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		part := data
		if end >= 0 {
			part = data[:end]
		}
		if !overflow {
			if len(line)+len(part) > MaxReportBytes {
				overflow, line = true, line[:0]
			} else {
				line = append(line, part...)
			}
		}
		if end < 0 {
			break
		}
		if !overflow {
			if text := bytes.TrimSpace(line); len(text) > 0 {
				accept(text)
			}
		} else {
			accept(nil) // counted as a rejected report
		}
		line, overflow = line[:0], false
		data = data[end+1:]
	}
	return line, overflow
}

// FromNetwork: the peer of the TCP connection (not a forwarded header) is on
// the USB network, and is not the NanoKVM itself.
func FromNetwork(remoteAddr string, nets []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) && !n.IP.Equal(ip) {
			return true
		}
	}
	return false
}
