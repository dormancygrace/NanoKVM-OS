package rustdesk

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/inputcontrol"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/ws"
	log "github.com/sirupsen/logrus"
)

// OKHC v1, the add-on's input and control channel on control.sock. Each
// RustDesk session keeps one connection. It starts with
//
//	"OKHC", version, reserved[3], session[16]
//
// and then carries frames: a count of 1 to 8 and that many 8-byte records,
// a kind and 7 payload bytes, unused ones zero:
//
//	1 keyboard   modifiers, keys[6]
//	2 relative   buttons, x, y, wheel (signed bytes)
//	3 absolute   buttons, x u16le, y u16le (0..32767), wheel
//	4 heartbeat
//	5 close
//	6 prepare    USB preparation; belongs to no session
//
// Every frame is answered with one status byte. The records of a frame run in
// order and share its two seconds; the first one that fails ends the frame
// and its status is the answer.
const (
	controlVersion    = 1
	controlRecordSize = 8
	controlMaxRecords = 8

	recordKeyboard  = 1
	recordRelative  = 2
	recordAbsolute  = 3
	recordHeartbeat = 4
	recordClose     = 5
	recordPrepare   = 6

	statusOK        = 0
	statusLeaseHeld = 1 // errLeaseHeld
	statusRejected  = 2 // the session lost input: preempted, revoked or blocked
	statusInvalid   = 3
	statusTimeout   = 4
	statusFailed    = 5 // the USB write or preparation failed
)

// Twice the eight sessions: a session that gave a frame up connects again
// while its old connection may still be finishing that frame.
const controlMaxConnections = 16

func (b *Bridge) acceptControl(listener net.Listener) {
	for {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		if b.control != listener || len(b.controlConnections) >= controlMaxConnections {
			b.mu.Unlock()
			c.Close()
			continue
		}
		b.controlConnections[c] = struct{}{}
		b.mu.Unlock()
		go func() {
			defer func() { c.Close(); b.mu.Lock(); delete(b.controlConnections, c); b.mu.Unlock() }()
			b.serveControl(c)
		}()
	}
}

// A connection that ends leaves its session to the heartbeat expiry, as a
// session whose add-on stopped sending always was: the add-on may reconnect
// for the same session, and its held keys must not be released under it.
func (b *Bridge) serveControl(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReaderSize(c, 128)
	var hello [24]byte
	if _, err := io.ReadFull(reader, hello[:]); err != nil || string(hello[:4]) != "OKHC" || hello[4] != controlVersion {
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	id := hex.EncodeToString(hello[8:])
	var frame [controlRecordSize * controlMaxRecords]byte
	for {
		count, err := reader.ReadByte()
		if err != nil || count == 0 || count > controlMaxRecords {
			return
		}
		records := frame[:controlRecordSize*int(count)]
		if _, err := io.ReadFull(reader, records); err != nil {
			return
		}
		frame[0] = b.controlFrame(id, records)
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write(frame[:1]); err != nil {
			return
		}
	}
}

func (b *Bridge) controlFrame(id string, records []byte) byte {
	if records[0] == recordPrepare {
		b.mu.Lock()
		prepare := b.prepareUSB
		b.mu.Unlock()
		if len(records) != controlRecordSize {
			return statusInvalid
		}
		if prepare == nil {
			return statusFailed
		}
		if err := prepare(); err != nil {
			log.Errorf("RustDesk USB preparation failed: %s", err)
			return statusFailed
		}
		return statusOK
	}
	// The add-on gives a frame up after three seconds and then releases what
	// it held on a new connection. Nothing of this frame may be written after
	// that, so all its records share one deadline before it.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for ; len(records) > 0; records = records[controlRecordSize:] {
		if status := b.controlRecord(ctx, id, records[:controlRecordSize]); status != statusOK {
			return status
		}
	}
	return statusOK
}

func (b *Bridge) controlRecord(ctx context.Context, id string, record []byte) byte {
	recordKind := record[0]
	if recordKind < recordKeyboard || recordKind > recordClose {
		return statusInvalid
	}
	// Only a report takes the input lease; a heartbeat or close of a session
	// that has none is answered without one.
	s, err := b.session(id, recordKind <= recordAbsolute)
	if errors.Is(err, errLeaseHeld) {
		return statusLeaseHeld
	} else if err != nil {
		return statusRejected
	}
	if recordKind == recordClose {
		if s != nil {
			s.close()
			b.mu.Lock()
			delete(b.sessions, id)
			b.mu.Unlock()
		}
		return statusOK
	}
	if s == nil {
		return statusOK
	}
	s.mu.Lock()
	if s.closed.Load() {
		s.mu.Unlock()
		return statusRejected
	}
	s.touched = time.Now()
	if recordKind == recordHeartbeat {
		s.mu.Unlock()
		return statusOK
	}
	defer s.mu.Unlock()
	var report []byte
	var extraReports [][]byte
	kind := inputcontrol.ManualRelativeMouse
	switch recordKind {
	case recordKeyboard:
		report = make([]byte, 8)
		report[0] = record[1]
		copy(report[2:], record[2:8])
		kind = inputcontrol.ManualKeyboard
	case recordAbsolute:
		x, y := int32(binary.LittleEndian.Uint16(record[2:4])), int32(binary.LittleEndian.Uint16(record[4:6]))
		if x > 32767 || y > 32767 {
			return statusInvalid
		}
		// The record's payload is the USB report up to its padding byte.
		report = make([]byte, 7)
		copy(report, record[1:7])
		kind = inputcontrol.ManualAbsoluteMouse
		if s.relativeOnly {
			reports := s.relativePointer.reports(record[1], x, y, int8(record[6]))
			report, extraReports = reports[0], reports[1:]
			kind = inputcontrol.ManualRelativeMouse
		}
	default:
		if record[2] == 0x80 || record[3] == 0x80 {
			return statusInvalid
		}
		report = []byte{record[1], record[2], record[3], record[4], 0}
	}
	held := reportHeld(kind, report)
	allow := func(mode controlmode.Mode) bool {
		return ws.GetManager().AllowsInputLease(id) && (mode != controlmode.ModePicoclaw || !picoclaw.GetSessionLock().BlocksManualInput())
	}
	reservation, err := s.manual.Reserve(ctx, kind, held, allow)
	if err != nil {
		return statusRejected
	}
	// The RustDesk input task already serializes events. Execute USB writes in
	// the connection's goroutine instead of adding another scheduling hop
	// through HID workers.
	started := false
	err = reservation.Execute(func() error {
		for _, next := range append([][]byte{report}, extraReports...) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.closed.Load() || !ws.GetManager().AllowsInputLease(id) {
				return inputcontrol.ErrManualInputBlocked
			}
			started = true
			if err := s.writeReport(kind, next); err != nil {
				if cleanupErr := s.releaseHeld(); cleanupErr != nil {
					log.Errorf("RustDesk HID write cleanup failed: %s", cleanupErr)
				}
				return err
			}
		}
		return nil
	})
	reservation.Complete(err == nil)
	if err == nil {
		return statusOK
	}
	log.Errorf("RustDesk HID report failed: record=%d started=%t error=%s", recordKind, started, err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return statusTimeout
	} else if errors.Is(err, inputcontrol.ErrManualInputBlocked) {
		return statusRejected
	}
	return statusFailed
}
