// Package ipmi serves IPMI v2.0 over LAN (RMCP+, ipmitool's "lanplus"
// interface) for power control: enough for `ipmitool chassis power
// status|on|off|cycle|reset|soft` and fence_ipmilan. IPMI v1.5 sessions and
// unauthenticated or unencrypted cipher suites are not served.
//
// RMCP+ hands a hash of the user's password to anyone who asks to log in
// (RAKP message 2), which an attacker can brute-force offline: the add-on
// keeps IPMI off unless the user turns it on.
package ipmi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Power is what the server needs from bmc.Power.
type Power interface {
	Status(ctx context.Context) (on, known bool, err error)
	Available(ctx context.Context) (power, reset bool, err error)
	On(ctx context.Context) error
	ForceOff(ctx context.Context) error
	Soft(ctx context.Context) error
	Reset(ctx context.Context) error
	Cycle(ctx context.Context) error
}

const (
	rmcpVersion = 0x06
	classASF    = 0x06
	classIPMI   = 0x07

	authTypeNone = 0x00
	authTypeRMCP = 0x06

	payloadIPMI          = 0x00
	payloadOpenRequest   = 0x10
	payloadOpenResponse  = 0x11
	payloadRAKP1         = 0x12
	payloadRAKP2         = 0x13
	payloadRAKP3         = 0x14
	payloadRAKP4         = 0x15
	payloadEncrypted     = 0x80
	payloadAuthenticated = 0x40

	netFnChassis = 0x00
	netFnApp     = 0x06

	privOperator = 3
	privAdmin    = 4

	maxSessions    = 8
	sessionTimeout = time.Minute
	pendingTimeout = 10 * time.Second
	// Logins (RAKP message 1) per client and minute: plenty for a fence
	// agent, which logs in for each action, few for a hash harvester.
	maxFailures = 20
	failWindow  = time.Minute
)

// RAKP status codes (IPMI v2.0, table 13-15).
const (
	statusOK                  = 0x00
	statusNoResources         = 0x01
	statusInvalidSession      = 0x02
	statusInvalidRole         = 0x09
	statusUnauthorizedName    = 0x0D
	statusInvalidIntegrity    = 0x0F
	statusInvalidAuthAlg      = 0x10
	statusInvalidIntegrityAlg = 0x11
)

// Completion codes.
const (
	ccOK               = 0x00
	ccNodeBusy         = 0xC0
	ccInvalidCommand   = 0xC1
	ccRequestTooShort  = 0xC7
	ccOutOfRange       = 0xC9
	ccInvalidField     = 0xCC
	ccInsufficientPriv = 0xD4
	ccNotInThisState   = 0xD5
	ccPrivExceeded     = 0x81
)

type session struct {
	managedID, consoleID uint32
	suite                suite
	active               bool
	maxPriv, priv        byte
	rm, rc               [16]byte
	role                 byte
	name                 []byte
	sik, k1, k2          []byte
	seqOut               uint32
	seqIn                uint32 // highest sequence number received
	seen                 uint32 // bit i: seqIn-i was received
	lastUsed             time.Time
	addr                 string
}

type failures struct {
	count int
	since time.Time
}

// Server answers RMCP and RMCP+ packets.
type Server struct {
	Username, Password string
	GUID               [16]byte
	// FirmwareMajor and FirmwareMinor are reported by Get Device ID.
	FirmwareMajor, FirmwareMinor byte
	Power                        Power
	Log                          func(format string, args ...any)

	now func() time.Time
	mu  sync.Mutex
	// lastExpire is the last expiry scan: at most one a second, not one
	// per packet (an unauthenticated sender chooses the packet rate).
	lastExpire time.Time
	sessions   map[uint32]*session
	failed     map[string]*failures
	actions    chan func(context.Context) error
	start      sync.Once
	// The last power command accepted, for Stats.
	lastCommand   string
	lastCommandAt time.Time
}

// Stats is what the add-on's settings page shows.
type Stats struct {
	// Sessions is the number of established, unexpired IPMI sessions.
	Sessions int `json:"sessions"`
	// LockedOut is the number of clients refused for too many logins.
	LockedOut int `json:"lockedOut"`
	// LastCommand is the last power command accepted ("power on", ...).
	LastCommand   string    `json:"lastCommand"`
	LastCommandAt time.Time `json:"lastCommandAt"`
}

// Stats reports the server's sessions and its last power command.
func (s *Server) Stats() Stats {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	stats := Stats{LastCommand: s.lastCommand, LastCommandAt: s.lastCommandAt}
	// Expiry runs with the packets: count what it would keep.
	for _, se := range s.sessions {
		if se.active && now.Sub(se.lastUsed) <= sessionTimeout {
			stats.Sessions++
		}
	}
	for _, f := range s.failed {
		if f.count >= maxFailures && now.Sub(f.since) <= failWindow {
			stats.LockedOut++
		}
	}
	return stats
}

func (s *Server) init() {
	s.start.Do(func() {
		if s.now == nil {
			s.now = time.Now
		}
		s.sessions = map[uint32]*session{}
		s.failed = map[string]*failures{}
		// Power actions run one at a time after the reply: a ForceOff holds
		// the button for seconds, longer than ipmitool waits for an answer.
		s.actions = make(chan func(context.Context) error, 1)
		go func() {
			for action := range s.actions {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := action(ctx); err != nil && s.Log != nil {
					s.Log("IPMI power action failed: %v", err)
				}
				cancel()
			}
		}()
	})
}

// Serve answers on conn until it is closed.
func (s *Server) Serve(conn net.PacketConn) error {
	s.init()
	buf := make([]byte, 1024)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			return err
		}
		if reply := s.Handle(buf[:n], addr.String()); reply != nil {
			_, _ = conn.WriteTo(reply, addr)
		}
	}
}

func rmcpHeader(class byte) []byte { return []byte{rmcpVersion, 0x00, 0xFF, class} }

// Handle answers one packet; nil means no answer.
func (s *Server) Handle(pkt []byte, addr string) []byte {
	s.init()
	if len(pkt) < 5 || pkt[0] != rmcpVersion {
		return nil
	}
	switch pkt[3] & 0x1F {
	case classASF:
		return s.asf(pkt[4:])
	case classIPMI:
		switch pkt[4] {
		case authTypeNone:
			return s.v15(pkt[4:])
		case authTypeRMCP:
			return s.v20(pkt[4:], addr)
		}
	}
	return nil
}

// asf answers an ASF presence ping, which some tools send first.
func (s *Server) asf(msg []byte) []byte {
	if len(msg) < 8 || binary.BigEndian.Uint32(msg) != 4542 || msg[4] != 0x80 {
		return nil
	}
	pong := append(rmcpHeader(classASF), 0x00, 0x00, 0x11, 0xBE, 0x40, msg[5], 0x00, 0x10)
	pong = append(pong, 0x00, 0x00, 0x11, 0xBE, 0, 0, 0, 0, 0x81, 0x00, 0, 0, 0, 0, 0, 0)
	return pong
}

func checksum(b []byte) byte {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return -sum
}

// request is an IPMI LAN message.
type request struct {
	netFn, cmd, rqSeqLun, rqAddr, rsLun byte
	data                                []byte
}

func parseMessage(m []byte) (request, bool) {
	if len(m) < 7 || checksum(m[:2]) != m[2] || checksum(m[3:len(m)-1]) != m[len(m)-1] {
		return request{}, false
	}
	return request{netFn: m[1] >> 2, rsLun: m[1] & 3, rqAddr: m[3], rqSeqLun: m[4], cmd: m[5], data: m[6 : len(m)-1]}, true
}

func (r request) reply(cc byte, data ...byte) []byte {
	head := []byte{r.rqAddr, (r.netFn|1)<<2 | r.rqSeqLun&3}
	head = append(head, checksum(head))
	body := append([]byte{0x20, r.rqSeqLun&^3 | r.rsLun, r.cmd, cc}, data...)
	return append(append(head, body...), checksum(body))
}

// v15 answers the pre-session commands ipmitool sends in IPMI v1.5 packets
// before an RMCP+ session: Get Channel Authentication Capabilities and Get
// Channel Cipher Suites.
func (s *Server) v15(p []byte) []byte {
	if len(p) < 10 || binary.BigEndian.Uint32(p[5:9]) != 0 {
		return nil
	}
	length := int(p[9])
	if len(p) < 10+length {
		return nil
	}
	req, ok := parseMessage(p[10 : 10+length])
	if !ok || req.netFn != netFnApp {
		return nil
	}
	var msg []byte
	switch req.cmd {
	case 0x38:
		msg = req.reply(ccOK, authCapabilities()...)
	case 0x54:
		cc, data := cipherSuites(req.data)
		msg = req.reply(cc, data...)
	default:
		msg = req.reply(ccInsufficientPriv)
	}
	out := append(rmcpHeader(classIPMI), authTypeNone, 0, 0, 0, 0, 0, 0, 0, 0, byte(len(msg)))
	return append(out, msg...)
}

func authCapabilities() []byte {
	// Channel 1; IPMI v2.0 extended data; no v1.5 authentication types;
	// non-null user names only; IPMI v2.0 connections supported.
	return []byte{0x01, 0x80, 0x04, 0x02, 0x00, 0x00, 0x00, 0x00}
}

func cipherSuites(data []byte) (byte, []byte) {
	if len(data) < 3 {
		return ccRequestTooShort, nil
	}
	if data[1] != payloadIPMI {
		return ccInvalidField, nil
	}
	var records []byte
	for _, su := range suites {
		records = append(records, 0xC0, su.id, su.auth, 0x40|su.integrity, 0x80|confAESCBC128)
	}
	start := int(data[2]&0x3F) * 16
	if start > len(records) {
		return ccOutOfRange, nil
	}
	end := min(start+16, len(records))
	return ccOK, append([]byte{0x01}, records[start:end]...)
}

// clientKey is the login counter of a packet's source: its host, and for
// IPv6 its /64, so that one host cannot rotate through its prefix.
func clientKey(addr string) string {
	host, _, _ := net.SplitHostPort(addr)
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is6() && !ip.Is4In6() {
		if prefix, err := ip.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return host
}

func (s *Server) lockedOut(addr string) bool {
	host := clientKey(addr)
	f := s.failed[host]
	if f == nil {
		return false
	}
	if s.now().Sub(f.since) > failWindow {
		delete(s.failed, host)
		return false
	}
	return f.count >= maxFailures
}

func (s *Server) recordFailure(addr string) {
	host := clientKey(addr)
	f := s.failed[host]
	if f == nil || s.now().Sub(f.since) > failWindow {
		if f == nil && len(s.failed) >= maxTracked {
			s.evictOldestFailure()
		}
		f = &failures{since: s.now()}
		s.failed[host] = f
	}
	f.count++
}

// maxTracked bounds the login counters: UDP source addresses are easy to
// forge, so a spray could otherwise grow the table without limit.
const maxTracked = 1024

// evictOldestFailure makes room for one more login counter.
func (s *Server) evictOldestFailure() {
	var oldest string
	var since time.Time
	for host, f := range s.failed {
		if oldest == "" || f.since.Before(since) {
			oldest, since = host, f.since
		}
	}
	delete(s.failed, oldest)
}

// expireEvery runs expire at most once per interval.
func (s *Server) expireEvery(interval time.Duration) {
	if now := s.now(); now.Sub(s.lastExpire) >= interval || now.Before(s.lastExpire) {
		s.lastExpire = now
		s.expire()
	}
}

func (s *Server) expire() {
	now := s.now()
	for host, f := range s.failed {
		if now.Sub(f.since) > failWindow {
			delete(s.failed, host)
		}
	}
	for len(s.failed) > maxTracked {
		s.evictOldestFailure()
	}
	for id, se := range s.sessions {
		limit := sessionTimeout
		if !se.active {
			limit = pendingTimeout
		}
		if now.Sub(se.lastUsed) > limit {
			delete(s.sessions, id)
		}
	}
}

func (s *Server) newID() uint32 {
	var b [4]byte
	for {
		_, _ = rand.Read(b[:])
		id := binary.LittleEndian.Uint32(b[:])
		if id != 0 && s.sessions[id] == nil {
			return id
		}
	}
}

// v20 handles an RMCP+ packet (p starts at the authentication type byte).
func (s *Server) v20(p []byte, addr string) []byte {
	if len(p) < 12 {
		return nil
	}
	ptype := p[1] & 0x3F
	sessionID := binary.LittleEndian.Uint32(p[2:6])
	seq := binary.LittleEndian.Uint32(p[6:10])
	length := int(binary.LittleEndian.Uint16(p[10:12]))
	if len(p) < 12+length {
		return nil
	}
	payload := p[12 : 12+length]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireEvery(time.Second)
	switch ptype {
	case payloadOpenRequest:
		return s.openSession(payload, addr)
	case payloadRAKP1:
		return s.rakp1(payload, addr)
	case payloadRAKP3:
		return s.rakp3(payload, addr)
	case payloadIPMI:
		if sessionID == 0 {
			// ipmitool without -C asks for the cipher suites this way,
			// unauthenticated, before it opens a session.
			return s.preSessionMessage(payload)
		}
		se := s.sessions[sessionID]
		if se == nil || !se.active || se.addr != addr {
			return nil
		}
		return s.message(se, p, seq, length)
	}
	return nil
}

func (s *Server) preSession(ptype byte, payload []byte) []byte {
	out := append(rmcpHeader(classIPMI), authTypeRMCP, ptype, 0, 0, 0, 0, 0, 0, 0, 0)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(payload)))
	return append(out, payload...)
}

// preSessionMessage answers the two pre-session commands in an
// unauthenticated RMCP+ packet.
func (s *Server) preSessionMessage(p []byte) []byte {
	req, ok := parseMessage(p)
	if !ok || req.netFn != netFnApp {
		return nil
	}
	var msg []byte
	switch req.cmd {
	case 0x38:
		msg = req.reply(ccOK, authCapabilities()...)
	case 0x54:
		cc, data := cipherSuites(req.data)
		msg = req.reply(cc, data...)
	default:
		msg = req.reply(ccInsufficientPriv)
	}
	return s.preSession(payloadIPMI, msg)
}

func (s *Server) openSession(p []byte, addr string) []byte {
	if len(p) < 32 {
		return nil
	}
	tag, consoleID := p[0], binary.LittleEndian.Uint32(p[4:8])
	fail := func(status byte) []byte {
		reply := []byte{tag, status, 0, 0}
		return s.preSession(payloadOpenResponse, binary.LittleEndian.AppendUint32(reply, consoleID))
	}
	if p[8] != 0x00 || p[16] != 0x01 || p[24] != 0x02 {
		return fail(statusInvalidAuthAlg)
	}
	su, ok := suiteFor(p[12]&0x3F, p[20]&0x3F, p[28]&0x3F)
	if !ok {
		if p[12]&0x3F != authHMACSHA1 && p[12]&0x3F != authHMACSHA256 {
			return fail(statusInvalidAuthAlg)
		}
		return fail(statusInvalidIntegrityAlg)
	}
	maxPriv := p[1] & 0x0F
	if maxPriv == 0 || maxPriv > privAdmin {
		maxPriv = privAdmin
	}
	if len(s.sessions) >= maxSessions {
		// A login left halfway (a client that gave up after RAKP message 2)
		// makes room, so that unfinished logins cannot lock others out;
		// established sessions are kept.
		var oldest *session
		for _, se := range s.sessions {
			if !se.active && (oldest == nil || se.lastUsed.Before(oldest.lastUsed)) {
				oldest = se
			}
		}
		if oldest == nil {
			return fail(statusNoResources)
		}
		delete(s.sessions, oldest.managedID)
	}
	se := &session{managedID: s.newID(), consoleID: consoleID, suite: su, maxPriv: maxPriv, lastUsed: s.now(), addr: addr}
	s.sessions[se.managedID] = se
	reply := []byte{tag, statusOK, maxPriv, 0}
	reply = binary.LittleEndian.AppendUint32(reply, consoleID)
	reply = binary.LittleEndian.AppendUint32(reply, se.managedID)
	reply = append(reply, 0x00, 0, 0, 0x08, su.auth, 0, 0, 0)
	reply = append(reply, 0x01, 0, 0, 0x08, su.integrity, 0, 0, 0)
	reply = append(reply, 0x02, 0, 0, 0x08, confAESCBC128, 0, 0, 0)
	return s.preSession(payloadOpenResponse, reply)
}

// userKey is K_UID: the password padded to the 20 bytes of its field.
func (s *Server) userKey() []byte {
	key := make([]byte, 20)
	copy(key, s.Password)
	return key
}

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func (s *Server) rakp1(p []byte, addr string) []byte {
	if len(p) < 28 {
		return nil
	}
	tag, managedID := p[0], binary.LittleEndian.Uint32(p[4:8])
	se := s.sessions[managedID]
	if se == nil || se.active || se.addr != addr {
		return s.preSession(payloadRAKP2, append([]byte{tag, statusInvalidSession, 0, 0}, 0, 0, 0, 0))
	}
	fail := func(status byte) []byte {
		delete(s.sessions, managedID)
		return s.preSession(payloadRAKP2, append([]byte{tag, status, 0, 0}, le32(se.consoleID)...))
	}
	nameLen := int(p[27])
	if nameLen > 16 || len(p) < 28+nameLen {
		return fail(statusUnauthorizedName)
	}
	copy(se.rm[:], p[8:24])
	se.role = p[24]
	se.name = append([]byte(nil), p[28:28+nameLen]...)
	priv := se.role & 0x0F
	if priv == 0 || priv > se.maxPriv {
		return fail(statusInvalidRole)
	}
	// Every RAKP message 1 counts: a client with a wrong password gives up
	// after checking RAKP message 2 and never sends message 3, and each
	// message 2 hands out a password hash to brute-force offline.
	if s.lockedOut(addr) {
		return fail(statusUnauthorizedName)
	}
	s.recordFailure(addr)
	if !hmac.Equal(se.name, []byte(s.Username)) || s.Password == "" {
		return fail(statusUnauthorizedName)
	}
	_, _ = rand.Read(se.rc[:])
	se.lastUsed = s.now()
	code := se.suite.mac(s.userKey(), le32(se.consoleID), le32(se.managedID), se.rm[:], se.rc[:], s.GUID[:],
		[]byte{se.role, byte(nameLen)}, se.name)
	reply := append([]byte{tag, statusOK, 0, 0}, le32(se.consoleID)...)
	reply = append(append(append(reply, se.rc[:]...), s.GUID[:]...), code...)
	return s.preSession(payloadRAKP2, reply)
}

func (s *Server) rakp3(p []byte, addr string) []byte {
	if len(p) < 8 {
		return nil
	}
	tag, status, managedID := p[0], p[1], binary.LittleEndian.Uint32(p[4:8])
	se := s.sessions[managedID]
	if se == nil || se.active || se.addr != addr || se.name == nil {
		return s.preSession(payloadRAKP4, append([]byte{tag, statusInvalidSession, 0, 0}, 0, 0, 0, 0))
	}
	if status != statusOK {
		delete(s.sessions, managedID)
		return nil
	}
	want := se.suite.mac(s.userKey(), se.rc[:], le32(se.consoleID), []byte{se.role, byte(len(se.name))}, se.name)
	if !hmac.Equal(p[8:], want) {
		delete(s.sessions, managedID)
		s.recordFailure(addr)
		return s.preSession(payloadRAKP4, append([]byte{tag, statusInvalidIntegrity, 0, 0}, le32(se.consoleID)...))
	}
	se.sik = se.suite.mac(s.userKey(), se.rm[:], se.rc[:], []byte{se.role, byte(len(se.name))}, se.name)
	se.k1, se.k2 = se.suite.keys(se.sik)
	se.active = true
	se.priv = se.role & 0x0F
	se.lastUsed = s.now()
	icv := se.suite.mac(se.sik, se.rm[:], le32(se.managedID), s.GUID[:])[:se.suite.rakp4Len]
	reply := append([]byte{tag, statusOK, 0, 0}, le32(se.consoleID)...)
	return s.preSession(payloadRAKP4, append(reply, icv...))
}

// acceptSeq rejects replays: a sequence number already seen, or more than
// 15 behind the highest.
func (se *session) acceptSeq(seq uint32) bool {
	if seq == 0 {
		return false
	}
	switch {
	case seq > se.seqIn:
		shift := seq - se.seqIn
		if shift >= 32 {
			se.seen = 0
		} else {
			se.seen <<= shift
		}
		se.seen |= 1
		se.seqIn = seq
		return true
	case se.seqIn-seq < 16:
		bit := uint32(1) << (se.seqIn - seq)
		if se.seen&bit != 0 {
			return false
		}
		se.seen |= bit
		return true
	}
	return false
}

func (s *Server) message(se *session, p []byte, seq uint32, length int) []byte {
	ptype := p[1]
	if ptype&payloadAuthenticated == 0 || ptype&payloadEncrypted == 0 {
		return nil
	}
	n := se.suite.integLen
	if len(p) < 12+length+2+n {
		return nil
	}
	signed := p[:len(p)-n]
	if signed[len(signed)-1] != 0x07 || !hmac.Equal(p[len(p)-n:], se.suite.mac(se.k1, signed)[:n]) {
		return nil
	}
	if !se.acceptSeq(seq) {
		return nil
	}
	plain, err := decrypt(se.k2, p[12:12+length])
	if err != nil {
		return nil
	}
	req, ok := parseMessage(plain)
	if !ok {
		return nil
	}
	se.lastUsed = s.now()
	msg := s.command(se, req)
	if msg == nil {
		return nil
	}
	reply := s.sealed(se, msg)
	if req.netFn == netFnApp && req.cmd == 0x3C {
		if len(req.data) >= 4 && binary.LittleEndian.Uint32(req.data) == se.managedID {
			delete(s.sessions, se.managedID)
		}
	}
	return reply
}

// sealed encrypts and signs a reply in the session.
func (s *Server) sealed(se *session, msg []byte) []byte {
	payload, err := encrypt(se.k2, msg)
	if err != nil {
		return nil
	}
	se.seqOut++
	out := []byte{authTypeRMCP, payloadIPMI | payloadEncrypted | payloadAuthenticated}
	out = binary.LittleEndian.AppendUint32(out, se.consoleID)
	out = binary.LittleEndian.AppendUint32(out, se.seqOut)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(payload)))
	out = append(out, payload...)
	// Integrity pad: the signed part (from the authentication type to the
	// next header) is a multiple of 4 bytes.
	pad := (4 - (len(out)+2)%4) % 4
	for i := 0; i < pad; i++ {
		out = append(out, 0xFF)
	}
	out = append(out, byte(pad), 0x07)
	out = append(out, se.suite.mac(se.k1, out)[:se.suite.integLen]...)
	return append(rmcpHeader(classIPMI), out...)
}

func (s *Server) command(se *session, req request) []byte {
	switch {
	case req.netFn == netFnApp && req.cmd == 0x01: // Get Device ID
		return req.reply(ccOK, 0x20, 0x01, s.FirmwareMajor&0x7F, s.FirmwareMinor, 0x02, 0x80, 0, 0, 0, 0, 0)
	case req.netFn == netFnApp && req.cmd == 0x38:
		return req.reply(ccOK, authCapabilities()...)
	case req.netFn == netFnApp && req.cmd == 0x54: // Get Channel Cipher Suites
		cc, data := cipherSuites(req.data)
		return req.reply(cc, data...)
	case req.netFn == netFnApp && req.cmd == 0x3B: // Set Session Privilege Level
		if len(req.data) < 1 {
			return req.reply(ccRequestTooShort)
		}
		level := req.data[0] & 0x0F
		if level == 0 {
			return req.reply(ccOK, se.priv)
		}
		if level > se.maxPriv || level > se.role&0x0F {
			return req.reply(ccPrivExceeded)
		}
		se.priv = level
		return req.reply(ccOK, level)
	case req.netFn == netFnApp && req.cmd == 0x3C: // Close Session
		return req.reply(ccOK)
	case req.netFn == netFnChassis && req.cmd == 0x01: // Get Chassis Status
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		on, known, err := s.Power.Status(ctx)
		if err != nil || !known {
			return req.reply(ccNotInThisState)
		}
		state := byte(0x60) // power restore policy unknown
		if on {
			state |= 0x01
		}
		return req.reply(ccOK, state, 0x00, 0x00)
	case req.netFn == netFnChassis && req.cmd == 0x02: // Chassis Control
		return s.chassisControl(se, req)
	}
	return req.reply(ccInvalidCommand)
}

func (s *Server) chassisControl(se *session, req request) []byte {
	if se.priv < privOperator {
		return req.reply(ccInsufficientPriv)
	}
	if len(req.data) < 1 {
		return req.reply(ccRequestTooShort)
	}
	var action func(context.Context) error
	var name string
	switch req.data[0] & 0x0F {
	case 0x00:
		action, name = s.Power.ForceOff, "power off"
	case 0x01:
		action, name = s.Power.On, "power on"
	case 0x02:
		action, name = s.Power.Cycle, "power cycle"
	case 0x03:
		action, name = s.Power.Reset, "reset"
	case 0x05:
		action, name = s.Power.Soft, "soft off"
	default:
		return req.reply(ccInvalidField)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	power, reset, err := s.Power.Available(ctx)
	if err != nil || !power || (req.data[0]&0x0F == 0x03 && !reset) {
		return req.reply(ccNotInThisState)
	}
	select {
	case s.actions <- action:
		s.lastCommand, s.lastCommandAt = name, s.now()
		return req.reply(ccOK)
	default:
		return req.reply(ccNodeBusy)
	}
}
