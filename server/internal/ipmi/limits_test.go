package ipmi

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

// Exercise the actual OpenSession/RAKP1 path: changing the source address
// within a /64 must not obtain another password hash after the limit.
func TestRAKPIPv6PrefixLockoutAndExpiry(t *testing.T) {
	s := newServer(&fakePower{})
	now := time.Unix(1800000000, 0)
	s.now = func() time.Time { return now }
	su := suites[0]
	login := func(source string) byte {
		t.Helper()
		open := []byte{1, privAdmin, 0, 0}
		open = append(open, le32(1234)...)
		open = append(open, 0, 0, 0, 8, su.auth, 0, 0, 0, 1, 0, 0, 8, su.integrity, 0, 0, 0, 2, 0, 0, 8, confAESCBC128, 0, 0, 0)
		reply := s.Handle(rmcp(authTypeRMCP, payloadOpenRequest, 0, 0, open), source)
		if len(reply) < 28 || reply[17] != statusOK {
			t.Fatalf("OpenSession failed: %x", reply)
		}
		sid := binary.LittleEndian.Uint32(reply[24:28])
		rakp := append([]byte{2, 0, 0, 0}, le32(sid)...)
		rakp = append(rakp, make([]byte, 16)...)
		rakp = append(rakp, 0x14, 0, 0, 5)
		rakp = append(rakp, "admin"...)
		reply = s.Handle(rmcp(authTypeRMCP, payloadRAKP1, 0, 0, rakp), source)
		if len(reply) < 18 {
			t.Fatalf("RAKP1 gave no valid reply: %x", reply)
		}
		return reply[17]
	}
	for i := 0; i < maxFailures; i++ {
		if code := login(fmt.Sprintf("[2001:db8:1:2::%x]:40000", i+1)); code != statusOK {
			t.Fatalf("attempt %d: status %#x", i+1, code)
		}
	}
	if code := login("[2001:db8:1:2::ffff]:40000"); code != statusUnauthorizedName {
		t.Fatalf("same /64 escaped lockout: %#x", code)
	}
	if code := login("[2001:db8:1:3::1]:40000"); code != statusOK {
		t.Fatalf("different /64 was blocked: %#x", code)
	}
	now = now.Add(failWindow + time.Nanosecond)
	if code := login("[2001:db8:1:2::ffff]:40000"); code != statusOK {
		t.Fatalf("expired prefix lockout persisted: %#x", code)
	}
}
