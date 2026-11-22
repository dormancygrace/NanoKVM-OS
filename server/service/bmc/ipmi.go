package bmc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/internal/ipmi"
	"NanoKVM-Server/journal"
	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/notify"
)

// ipmiRetry is how often IPMI that is on but cannot open its port tries
// again after the server starts: during an update the IPMI daemon of the
// previous release may hold the port until it stops.
const ipmiRetry = 5 * time.Second

// ipmiRun is IPMI over LAN while it runs: its UDP socket and its protocol
// server (internal/ipmi), with the settings it started with.
type ipmiRun struct {
	config bmc.Config
	server *ipmi.Server
	conn   net.PacketConn
	done   chan struct{}
	// handOver: the server stops to give the socket to its successor.
	handOver atomic.Bool
}

// running reports whether r serves; Serve ends when its socket fails.
func (r *ipmiRun) running() bool {
	if r == nil {
		return false
	}
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

// sameIPMI reports whether a and b serve IPMI alike; the power LED setting
// is read at each request.
func sameIPMI(a, b bmc.Config) bool {
	return a.Username == b.Username && a.Password == b.Password &&
		a.IPMIPort == b.IPMIPort && a.HoldSeconds == b.HoldSeconds
}

// ipmiSocket opens the UDP port of config when IPMI is on and the running
// listener does not have it already; nil means no new socket is needed.
// s.mu is held.
func (s *Service) ipmiSocket(config bmc.Config) (net.PacketConn, error) {
	if !config.IPMI || (s.ipmi.running() && s.ipmi.config.IPMIPort == config.IPMIPort) {
		return nil, nil
	}
	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(config.IPMIPort))
	if err != nil {
		return nil, fmt.Errorf("IPMI cannot use UDP port %d: %w", config.IPMIPort, err)
	}
	return conn, nil
}

// applyIPMI makes IPMI serve config, which is saved; conn is ipmiSocket's
// socket for it. IPMI that runs with the same account, port and hold keeps
// its sessions; a new account or hold on the same port takes over the
// socket. s.mu is held.
func (s *Service) applyIPMI(config bmc.Config, conn net.PacketConn) {
	switch {
	case !config.IPMI:
		s.stopIPMI()
	case conn != nil:
		s.stopIPMI()
		s.serveIPMI(config, conn)
	case !sameIPMI(s.ipmi.config, config):
		run := s.ipmi
		run.handOver.Store(true)
		_ = run.conn.SetReadDeadline(time.Now())
		<-run.done
		_ = run.conn.SetReadDeadline(time.Time{})
		s.serveIPMI(config, run.conn)
	}
}

// serveIPMI serves IPMI for config on conn; s.mu is held.
func (s *Service) serveIPMI(config bmc.Config, conn net.PacketConn) {
	major, minor := versionBytes(s.info.FirmwareVersion)
	server := &ipmi.Server{
		Username: config.Username, Password: config.Password, GUID: deviceGUID(),
		FirmwareMajor: major, FirmwareMinor: minor, Log: log.Warnf,
		Power:   &bmc.Power{ATX: s.atx, Hold: time.Duration(config.HoldSeconds) * time.Second}, // 0: the long press
		Context: journal.WithActor(context.Background(), "IPMI"),
		OnEvent: notifyIPMIEvent,
	}
	run := &ipmiRun{config: config, server: server, conn: conn, done: make(chan struct{})}
	go func() {
		defer close(run.done)
		err := server.Serve(conn)
		if run.handOver.Load() {
			return
		}
		// The socket is closed by stopIPMI, or failed: free the port.
		_ = conn.Close()
		if !errors.Is(err, net.ErrClosed) {
			log.Errorf("IPMI stopped: %v", err)
		}
	}()
	s.ipmi = run
	log.Infof("IPMI: listening on UDP port %d", config.IPMIPort)
}

// stopIPMI closes the port and waits for the server to end; s.mu is held.
func (s *Service) stopIPMI() {
	if s.ipmi == nil {
		return
	}
	_ = s.ipmi.conn.Close()
	<-s.ipmi.done
	s.ipmi = nil
}

// keepIPMI starts IPMI that is on at server start, trying again every
// ipmiRetry while its port is taken, until it runs or is switched off.
func (s *Service) keepIPMI() {
	for logged := false; ; time.Sleep(ipmiRetry) {
		s.mu.Lock()
		config, err := bmc.Load(s.configPath)
		if err == nil {
			err = config.Validate()
		}
		if err != nil || !config.IPMI || s.ipmi.running() {
			s.mu.Unlock()
			return
		}
		conn, err := s.ipmiSocket(config)
		if err == nil {
			s.applyIPMI(config, conn)
		}
		s.mu.Unlock()
		if err == nil {
			return
		}
		if !logged {
			log.Errorf("%v; trying again", err)
			logged = true
		}
	}
}

func notifyIPMIEvent(e ipmi.Event) {
	switch e.Kind {
	case "lockout":
		// IPMI counts every login, failed or not: its own message.
		notify.LoginAttemptsLimited(e.Address, e.Attempts, e.Minutes)
	case "login":
		// Its own list, apart from a web or Redfish user of the same name.
		notify.LoginSucceeded(e.User+" (IPMI)", e.Address)
	}
}

// deviceGUID is /etc/machine-id, the device's random identity, as 16 bytes.
func deviceGUID() [16]byte {
	var guid [16]byte
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil {
		if b, err := hex.DecodeString(strings.TrimSpace(string(data))); err == nil && len(b) == 16 {
			copy(guid[:], b)
		}
	}
	return guid
}

// versionBytes gives Get Device ID's firmware revision: the major number and
// the minor number in BCD.
func versionBytes(version string) (byte, byte) {
	parts := strings.SplitN(version, ".", 3)
	major, _ := strconv.Atoi(parts[0])
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	minor %= 100
	return byte(major & 0x7F), byte(minor/10<<4 | minor%10)
}
