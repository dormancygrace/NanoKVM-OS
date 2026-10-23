// Package storageidentity defines the SCSI INQUIRY names of virtual USB disks.
package storageidentity

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Identity struct {
	Vendor string `json:"vendor"`
	Image  string `json:"image"`
	Setup  string `json:"setup"`
	User   string `json:"user"`
}

var Defaults = Identity{Vendor: "NanoKVM", Setup: "Setup Disk", User: "User Disk"}
var Files = []string{"usb.storage.vendor", "usb.storage.image", "usb.storage.setup", "usb.storage.user"}

func valid(s string, limit int) bool {
	if len(s) > limit || strings.TrimSpace(s) != s {
		return false
	}
	for _, b := range []byte(s) {
		if b < 32 || b > 126 {
			return false
		}
	}
	return true
}
func (i Identity) Validate() error {
	for n, s := range []string{i.Vendor, i.Image, i.Setup, i.User} {
		limit := 16
		if n == 0 {
			limit = 8
		}
		if !valid(s, limit) {
			return fmt.Errorf("storage identity fields must be printable ASCII, at most %d characters, without leading or trailing spaces", limit)
		}
	}
	return nil
}
func (i Identity) Normalized() Identity {
	if i.Vendor == "" {
		i.Vendor = Defaults.Vendor
	}
	if i.Setup == "" {
		i.Setup = Defaults.Setup
	}
	if i.User == "" {
		i.User = Defaults.User
	}
	return i
}
func (i Identity) FileValues() map[string]*string {
	i = i.Normalized()
	out := map[string]*string{}
	values := []string{i.Vendor, i.Image, i.Setup, i.User}
	defaults := []string{Defaults.Vendor, Defaults.Image, Defaults.Setup, Defaults.User}
	for n, name := range Files {
		out[name] = nil
		if values[n] != defaults[n] {
			v := values[n]
			out[name] = &v
		}
	}
	return out
}
func Read(dir string) Identity {
	read := func(name string, limit int) string {
		// Read at most one valid line; do not load an unbounded corrupt config.
		f, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return ""
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, int64(limit+2)))
		if err != nil {
			return ""
		}
		s := strings.TrimSuffix(string(b), "\n")
		if !valid(s, limit) {
			return ""
		}
		return s
	}
	return (Identity{read(Files[0], 8), read(Files[1], 16), read(Files[2], 16), read(Files[3], 16)}).Normalized()
}

// Image's empty value keeps the mode-specific default. All fields are padded
// to the protocol's 8-byte vendor, 16-byte product and 4-byte revision slots.
func (i Identity) Inquiry(kind string) string {
	i = i.Normalized()
	product := i.Image
	switch kind {
	case "setup":
		product = i.Setup
	case "user":
		product = i.User
	case "cdrom":
		if product == "" {
			product = "USB CD/DVD-ROM"
		}
	case "remote":
		if product == "" {
			product = "Remote ISO"
		}
	default:
		if product == "" {
			product = "USB Mass Storage"
		}
	}
	return fmt.Sprintf("%-8s%-16s0520", i.Vendor, product)
}
