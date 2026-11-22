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

// applyIPMI starts, restarts or stops IPMI for config; s.mu is held. IPMI
// that runs with the same account, port and hold keeps its sessions.
func (s *Service) applyIPMI(config bmc.Config) error {
	if !config.IPMI {
		s.stopIPMI()
		return nil
	}
	if s.ipmi.running() && sameIPMI(s.ipmi.config, config) {
		return nil
	}
	s.stopIPMI()
	return s.startIPMI(config)
}

// startIPMI opens the UDP port and serves IPMI; s.mu is held.
func (s *Service) startIPMI(config bmc.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(config.IPMIPort))
	if err != nil {
		return fmt.Errorf("IPMI cannot use UDP port %d: %w", config.IPMIPort, err)
	}
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
		if err := server.Serve(conn); !errors.Is(err, net.ErrClosed) {
			log.Errorf("IPMI stopped: %v", err)
		}
	}()
	s.ipmi = run
	log.Infof("IPMI: listening on UDP port %d", config.IPMIPort)
	return nil
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
		if err != nil || !config.IPMI || s.ipmi.running() {
			s.mu.Unlock()
			return
		}
		err = s.startIPMI(config)
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
