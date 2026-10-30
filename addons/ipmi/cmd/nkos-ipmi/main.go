// nkos-ipmi is the daemon of the nanokvm-ipmi add-on: IPMI over LAN for
// power control of the host. The server itself serves Redfish; this runs
// apart from it so that a fault in parsing untrusted UDP packets cannot
// stop the server and its video.
package main

import (
	"encoding/hex"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"NanoKVM-IPMI/internal/ipmi"
	"NanoKVM-Server/pkg/bmc"
)

func main() {
	log.SetFlags(0)
	config, err := bmc.Load(bmc.ConfigPath)
	if err == nil {
		err = config.Validate()
	}
	if err != nil {
		log.Fatalf("settings: %v", err)
	}
	if !config.IPMI {
		log.Print("IPMI is off; nothing to serve")
		return
	}
	conn, err := net.ListenPacket("udp", ":"+strconv.Itoa(config.IPMIPort))
	if err != nil {
		log.Fatalf("IPMI: %v", err)
	}
	major, minor := versionBytes(firmwareVersion())
	power := bmc.NewClient()
	power.Hold = time.Duration(config.HoldSeconds) * time.Second
	server := &ipmi.Server{
		Username: config.Username, Password: config.Password, GUID: deviceGUID(),
		FirmwareMajor: major, FirmwareMinor: minor, Log: log.Printf,
		Power: power,
	}
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(conn) }()
	log.Printf("IPMI: listening on UDP port %d", config.IPMIPort)
	// The settings page's data, through the server's add-on proxy. Without
	// it the page says the add-on is not running; IPMI itself goes on.
	if err := serveStatus(statusSocket, server, config.IPMIPort); err != nil {
		log.Printf("status page: %v", err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-signals:
		_ = os.Remove(statusSocket)
	case err := <-errs:
		_ = os.Remove(statusSocket)
		log.Fatalf("IPMI: %v", err)
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

// firmwareVersion is the NanoKVM OS release, for example "3.4.0".
func firmwareVersion() string {
	data, err := os.ReadFile("/etc/nanokvm-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "VERSION="); ok {
			return strings.TrimPrefix(strings.Trim(value, `"`), "v")
		}
	}
	return ""
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
