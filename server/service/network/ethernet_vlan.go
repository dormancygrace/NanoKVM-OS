package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ethernetVLANFile = "/boot/eth.vlan"

func readEthernetVLAN() (bool, int, error) {
	contents, err := os.ReadFile(ethernetVLANFile)
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	text := strings.TrimSpace(string(contents))
	id, err := strconv.Atoi(text)
	if text == "" || strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || err != nil || id < 1 || id > 4094 {
		return false, 0, fmt.Errorf("invalid Ethernet VLAN ID")
	}
	return true, id, nil
}

func writeEthernetVLAN(enabled bool, id int) error {
	if !enabled {
		if err := os.Remove(ethernetVLANFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to disable Ethernet VLAN: %w", err)
		}
		return nil
	}
	if id < 1 || id > 4094 {
		return fmt.Errorf("VLAN ID must be between 1 and 4094")
	}
	if err := os.MkdirAll(filepath.Dir(ethernetVLANFile), 0o755); err != nil {
		return fmt.Errorf("failed to create Ethernet configuration directory: %w", err)
	}
	tmpFile := ethernetVLANFile + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(strconv.Itoa(id)+"\n"), 0o600); err != nil {
		return fmt.Errorf("failed to write Ethernet VLAN: %w", err)
	}
	if err := os.Rename(tmpFile, ethernetVLANFile); err != nil {
		return fmt.Errorf("failed to save Ethernet VLAN: %w", err)
	}
	return nil
}
