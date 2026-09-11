package timeconfig

import (
	"encoding/binary"
	"errors"
	"net"
	"sync/atomic"
	"time"
)

var ntpSequence atomic.Uint32

// NTPSynchronized queries the local ntpd system status (mode 6, READVAR).
// The kernel STA_UNSYNC flag can remain set while ntpd disciplines the clock
// in userspace; it is not an authoritative indicator of daemon synchronization.
func NTPSynchronized() (bool, error) {
	conn, err := net.DialTimeout("udp4", "127.0.0.1:123", 750*time.Millisecond)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(750 * time.Millisecond)); err != nil {
		return false, err
	}
	seq := uint16(ntpSequence.Add(1))
	request := make([]byte, 12)
	request[0] = 4<<3 | 6 // NTPv4 control message
	request[1] = 2        // READVAR, system association 0
	binary.BigEndian.PutUint16(request[2:4], seq)
	if _, err = conn.Write(request); err != nil {
		return false, err
	}
	response := make([]byte, 2048)
	n, err := conn.Read(response)
	if err != nil {
		return false, err
	}
	return parseNTPStatus(response[:n], seq)
}

func parseNTPStatus(packet []byte, seq uint16) (bool, error) {
	if len(packet) < 12 || packet[0]&7 != 6 || (packet[0]>>3)&7 < 2 || packet[1]&0x80 == 0 || packet[1]&0x40 != 0 || packet[1]&0x1f != 2 || binary.BigEndian.Uint16(packet[2:4]) != seq || binary.BigEndian.Uint16(packet[6:8]) != 0 || binary.BigEndian.Uint16(packet[8:10]) != 0 {
		return false, errors.New("invalid local NTP status reply")
	}
	if int(binary.BigEndian.Uint16(packet[10:12])) > len(packet)-12 {
		return false, errors.New("truncated local NTP status reply")
	}
	status := binary.BigEndian.Uint16(packet[4:6])
	// Leap alarm 3 means unsynchronized; source 6 means an NTP peer was selected.
	// The system status is present even if the READVAR payload is fragmented.
	return status>>14 != 3 && (status>>8)&0x3f == 6, nil
}
