package dashboard

import (
	"encoding/binary"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Read the kernel link kind, independent of a user-chosen interface name.
func linkKinds() map[int]string {
	result := map[int]string{}
	raw, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return result
	}
	messages, err := syscall.ParseNetlinkMessage(raw)
	if err != nil {
		return result
	}
	for _, message := range messages {
		if message.Header.Type != syscall.RTM_NEWLINK || len(message.Data) < syscall.SizeofIfInfomsg {
			continue
		}
		index := int(binary.NativeEndian.Uint32(message.Data[4:8]))
		info := linkAttribute(message.Data[syscall.SizeofIfInfomsg:], unix.IFLA_LINKINFO)
		kind := linkAttribute(info, unix.IFLA_INFO_KIND)
		if len(kind) > 0 {
			result[index] = strings.TrimRight(string(kind), "\x00")
		}
	}
	return result
}
func linkAttribute(data []byte, want uint16) []byte {
	for len(data) >= 4 {
		length := int(binary.NativeEndian.Uint16(data[:2]))
		kind := binary.NativeEndian.Uint16(data[2:4]) & 0x3fff
		if length < 4 || length > len(data) {
			return nil
		}
		if kind == want {
			return data[4:length]
		}
		aligned := (length + 3) &^ 3
		if aligned > len(data) {
			return nil
		}
		data = data[aligned:]
	}
	return nil
}
