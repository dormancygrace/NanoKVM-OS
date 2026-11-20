package ipmi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/binary"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakePower struct {
	mu      sync.Mutex
	on      bool
	unknown bool // no trusted power LED
	calls   []string
}

func (f *fakePower) Status(context.Context) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on, !f.unknown, nil
}
func (f *fakePower) Available(context.Context) (bool, bool, error) { return true, true, nil }
func (f *fakePower) record(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return nil
}
func (f *fakePower) On(context.Context) error       { return f.record("on") }
func (f *fakePower) ForceOff(context.Context) error { return f.record("off") }
func (f *fakePower) Soft(context.Context) error     { return f.record("soft") }
func (f *fakePower) Reset(context.Context) error    { return f.record("reset") }
func (f *fakePower) Cycle(context.Context) error    { return f.record("cycle") }

const addr = "192.0.2.9:40000"

// console is a minimal RMCP+ client, written from the specification.
type console struct {
	t        *testing.T
	s        *Server
	su       suite
	user     string
	password string
	id, sid  uint32
	rm, rc   [16]byte
	k1, k2   []byte
	seq      uint32
}

func rmcp(authType, ptype byte, session, seq uint32, payload []byte) []byte {
	out := append([]byte{0x06, 0, 0xFF, 0x07, authType, ptype}, le32(session)...)
	out = append(out, le32(seq)...)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(payload)))
	return append(out, payload...)
}

func (c *console) send(ptype byte, payload []byte) []byte {
	reply := c.s.Handle(rmcp(authTypeRMCP, ptype, 0, 0, payload), addr)
	if reply == nil {
		c.t.Fatalf("no reply to payload type %#x", ptype)
	}
	return reply[16:]
}

func (c *console) login() byte {
	c.id = 0xA0A1A2A3
	open := []byte{0x01, 0x04, 0, 0}
	open = append(open, le32(c.id)...)
	open = append(open, 0x00, 0, 0, 0x08, c.su.auth, 0, 0, 0, 0x01, 0, 0, 0x08, c.su.integrity, 0, 0, 0, 0x02, 0, 0, 0x08, confAESCBC128, 0, 0, 0)
	r := c.send(payloadOpenRequest, open)
	if r[1] != statusOK {
		return r[1]
	}
	c.sid = binary.LittleEndian.Uint32(r[8:12])
	copy(c.rm[:], "console random..")
	role := byte(0x14) // administrator, name-only lookup
	rakp1 := append([]byte{0x02, 0, 0, 0}, le32(c.sid)...)
	rakp1 = append(append(rakp1, c.rm[:]...), role, 0, 0, byte(len(c.user)))
	rakp1 = append(rakp1, c.user...)
	r = c.send(payloadRAKP1, rakp1)
	if r[1] != statusOK {
		return r[1]
	}
	copy(c.rc[:], r[8:24])
	guid := r[24:40]
	key := make([]byte, 20)
	copy(key, c.password)
	want := c.su.mac(key, le32(c.id), le32(c.sid), c.rm[:], c.rc[:], guid, []byte{role, byte(len(c.user))}, []byte(c.user))
	if !hmac.Equal(r[40:], want) {
		// A wrong password shows here; ipmitool gives up at this point.
		return statusInvalidIntegrity
	}
	code := c.su.mac(key, c.rc[:], le32(c.id), []byte{role, byte(len(c.user))}, []byte(c.user))
	r = c.send(payloadRAKP3, append(append([]byte{0x03, 0, 0, 0}, le32(c.sid)...), code...))
	if r[1] != statusOK {
		return r[1]
	}
	sik := c.su.mac(key, c.rm[:], c.rc[:], []byte{role, byte(len(c.user))}, []byte(c.user))
	if !hmac.Equal(r[8:], c.su.mac(sik, c.rm[:], le32(c.sid), guid)[:c.su.rakp4Len]) {
		c.t.Fatal("RAKP 4 check value does not verify")
	}
	c.k1, c.k2 = c.su.keys(sik)
	return statusOK
}

// command sends an IPMI request in the session and returns the completion
// code and data of the reply.
func (c *console) command(netFn, cmd byte, data ...byte) (byte, []byte) {
	head := []byte{0x20, netFn << 2}
	head = append(head, checksum(head))
	body := append([]byte{0x81, 0x04, cmd}, data...)
	msg := append(append(head, body...), checksum(body))
	payload, err := encrypt(c.k2, msg)
	if err != nil {
		c.t.Fatal(err)
	}
	c.seq++
	pkt := rmcp(authTypeRMCP, payloadIPMI|payloadEncrypted|payloadAuthenticated, c.sid, c.seq, payload)[4:]
	pad := (4 - (len(pkt)+2)%4) % 4
	pkt = append(pkt, bytes.Repeat([]byte{0xFF}, pad)...)
	pkt = append(pkt, byte(pad), 0x07)
	pkt = append(pkt, c.su.mac(c.k1, pkt)[:c.su.integLen]...)
	reply := c.s.Handle(append([]byte{0x06, 0, 0xFF, 0x07}, pkt...), addr)
	if reply == nil {
		return 0xFF, nil
	}
	p := reply[4:]
	n := c.su.integLen
	if !hmac.Equal(p[len(p)-n:], c.su.mac(c.k1, p[:len(p)-n])[:n]) {
		c.t.Fatal("reply integrity does not verify")
	}
	if binary.LittleEndian.Uint32(p[2:6]) != c.id {
		c.t.Fatal("reply addressed to another session")
	}
	length := int(binary.LittleEndian.Uint16(p[10:12]))
	plain, err := decrypt(c.k2, p[12:12+length])
	if err != nil {
		c.t.Fatal(err)
	}
	if checksum(plain[:2]) != plain[2] || checksum(plain[3:len(plain)-1]) != plain[len(plain)-1] {
		c.t.Fatal("bad reply checksums")
	}
	return plain[6], plain[7 : len(plain)-1]
}

func newServer(power *fakePower) *Server {
	s := &Server{Username: "admin", Password: "correct-horse", Power: power, FirmwareMajor: 3, FirmwareMinor: 0x04}
	copy(s.GUID[:], "0123456789abcdef")
	return s
}

func TestPowerControlBothSuites(t *testing.T) {
	for _, su := range suites {
		power := &fakePower{on: true}
		s := newServer(power)
		c := &console{t: t, s: s, su: su, user: "admin", password: "correct-horse"}
		if status := c.login(); status != statusOK {
			t.Fatalf("suite %d: login status %#x", su.id, status)
		}
		if cc, data := c.command(netFnApp, 0x3B, 0x04); cc != ccOK || data[0] != 0x04 {
			t.Fatalf("suite %d: set privilege %#x %v", su.id, cc, data)
		}
		if cc, data := c.command(netFnChassis, 0x01); cc != ccOK || data[0]&1 != 1 {
			t.Fatalf("suite %d: chassis status %#x %v", su.id, cc, data)
		}
		if cc, data := c.command(netFnApp, 0x01); cc != ccOK || data[2] != 3 || data[3] != 0x04 {
			t.Fatalf("suite %d: device id %#x %v", su.id, cc, data)
		}
		// The actions run after the reply, one at a time: wait for each.
		for i, control := range []byte{0x00, 0x01, 0x02, 0x03, 0x05} {
			if cc, _ := c.command(netFnChassis, 0x02, control); cc != ccOK {
				t.Fatalf("suite %d: chassis control %d: %#x", su.id, control, cc)
			}
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				power.mu.Lock()
				done := len(power.calls)
				power.mu.Unlock()
				if done == i+1 {
					break
				}
			}
		}
		power.mu.Lock()
		got := strings.Join(power.calls, " ")
		power.mu.Unlock()
		if got != "off on cycle reset soft" {
			t.Fatalf("suite %d: calls %v", su.id, got)
		}
		if cc, _ := c.command(netFnChassis, 0x02, 0x04); cc != ccInvalidField {
			t.Fatalf("suite %d: pulse diagnostic accepted", su.id)
		}
		if cc, _ := c.command(netFnApp, 0x3C, le32(c.sid)...); cc != ccOK {
			t.Fatalf("suite %d: close session %#x", su.id, cc)
		}
		if cc, _ := c.command(netFnChassis, 0x01); cc != 0xFF {
			t.Fatalf("suite %d: closed session still answers", su.id)
		}
	}
}

func TestWrongPasswordAndUser(t *testing.T) {
	s := newServer(&fakePower{})
	c := &console{t: t, s: s, su: suites[1], user: "admin", password: "wrong-password"}
	if status := c.login(); status != statusInvalidIntegrity {
		t.Fatalf("wrong password: status %#x", status)
	}
	c = &console{t: t, s: s, su: suites[1], user: "nobody", password: "correct-horse"}
	if status := c.login(); status != statusUnauthorizedName {
		t.Fatalf("unknown user: status %#x", status)
	}
}

func TestLockoutAfterFailures(t *testing.T) {
	s := newServer(&fakePower{})
	for i := 0; i < maxFailures; i++ {
		c := &console{t: t, s: s, su: suites[0], user: "admin", password: "guess"}
		c.login()
	}
	c := &console{t: t, s: s, su: suites[0], user: "admin", password: "correct-horse"}
	if status := c.login(); status != statusUnauthorizedName {
		t.Fatalf("locked out client logged in: %#x", status)
	}
}

func TestOperatorPrivilegeNeeded(t *testing.T) {
	s := newServer(&fakePower{})
	c := &console{t: t, s: s, su: suites[0], user: "admin", password: "correct-horse"}
	c.login()
	if cc, _ := c.command(netFnApp, 0x3B, 0x02); cc != ccOK {
		t.Fatalf("lower privilege: %#x", cc)
	}
	if cc, _ := c.command(netFnChassis, 0x02, 0x01); cc != ccInsufficientPriv {
		t.Fatalf("user-level chassis control: %#x", cc)
	}
}

func TestReplayIsDropped(t *testing.T) {
	var se session
	for _, seq := range []uint32{1, 2, 5} {
		if !se.acceptSeq(seq) {
			t.Fatalf("fresh %d rejected", seq)
		}
	}
	if se.acceptSeq(2) || se.acceptSeq(0) {
		t.Fatal("replay accepted")
	}
	if !se.acceptSeq(4) {
		t.Fatal("late but new 4 rejected")
	}
	se.acceptSeq(40)
	if se.acceptSeq(20) {
		t.Fatal("sequence too far behind accepted")
	}
}

func TestPresessionCommands(t *testing.T) {
	s := newServer(&fakePower{})
	// Get Channel Authentication Capabilities in an IPMI v1.5 packet.
	head := []byte{0x20, 0x06 << 2}
	head = append(head, checksum(head))
	body := []byte{0x81, 0x00, 0x38, 0x8E, 0x04}
	msg := append(append(head, body...), checksum(body))
	pkt := append([]byte{0x06, 0, 0xFF, 0x07, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(msg))}, msg...)
	reply := s.Handle(pkt, addr)
	if reply == nil || reply[14+6] != ccOK || reply[14+8]&0x80 == 0 {
		t.Fatalf("auth capabilities: %x", reply)
	}
	// Get Channel Cipher Suites lists suites 3 and 17.
	body = []byte{0x81, 0x00, 0x54, 0x0E, 0x00, 0x80}
	msg = append(append(head, body...), checksum(body))
	pkt = append([]byte{0x06, 0, 0xFF, 0x07, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(msg))}, msg...)
	reply = s.Handle(pkt, addr)
	records := reply[14+8 : len(reply)-1]
	if !bytes.Equal(records, []byte{0xC0, 3, 1, 0x41, 0x81, 0xC0, 17, 3, 0x44, 0x81}) {
		t.Fatalf("cipher suites %x", records)
	}
	// ASF presence ping.
	pong := s.Handle([]byte{0x06, 0, 0xFF, 0x06, 0, 0, 0x11, 0xBE, 0x80, 0x07, 0, 0}, addr)
	if pong == nil || pong[8] != 0x40 || pong[9] != 0x07 {
		t.Fatalf("pong %x", pong)
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 20)
	for n := 0; n < 40; n++ {
		plain := bytes.Repeat([]byte{byte(n)}, n)
		enc, err := encrypt(key, plain)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decrypt(key, enc)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("length %d: %v", n, err)
		}
	}
}

// Without a trusted power LED IPMI has no state to report: it answers with
// an error, never "off" (a fence agent would take that as done) and never
// "on" (it would skip a power-on).
func TestUnknownPowerStateIsAnError(t *testing.T) {
	s := newServer(&fakePower{unknown: true})
	c := &console{t: t, s: s, su: suites[0], user: "admin", password: "correct-horse"}
	if status := c.login(); status != statusOK {
		t.Fatalf("login status %#x", status)
	}
	if cc, data := c.command(netFnChassis, 0x01); cc != ccNotInThisState {
		t.Fatalf("chassis status %#x %v, want not in this state", cc, data)
	}
}
