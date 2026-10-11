package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"NanoKVM-Server/proto"
)

// The gadget's identity as the connected computer sees it: vendor and
// product IDs and the manufacturer and product strings. Hosts with USB
// allow-lists or endpoint security that flags "NanoKVM" need to see
// something else. The serial number stays the chip UID (the Linux console
// helper binds its udev rule to it), and bcdDevice stays the mode flag that
// hid.GetMode reads, so neither is part of the identity.
//
// S03usbdev and S03usbhid read the four files below from /boot at every
// start; a missing file means the default. The recovery init reads the same
// manufacturer and product files.
type usbIdentity struct {
	VendorID     string // 0x3346
	ProductID    string // 0x1009
	Manufacturer string
	Product      string
}

var defaultUSBIdentity = usbIdentity{VendorID: "0x3346", ProductID: "0x1009", Manufacturer: "sipeed", Product: "NanoKVM"}

// identityFromRequest checks a request's identity as typed, before any
// normalisation fills a bad value with the factory one: a vendor ID of
// "046d" is an error, not the factory VID. A nil request is the zero
// identity; the caller replaces it with the one in place.
func identityFromRequest(req *proto.USBIdentityReq) (usbIdentity, error) {
	if req == nil {
		return usbIdentity{}, nil
	}
	raw := usbIdentity{VendorID: req.VendorID, ProductID: req.ProductID, Manufacturer: req.Manufacturer, Product: req.Product}
	if err := raw.validate(); err != nil {
		return usbIdentity{}, err
	}
	return raw.normalized(), nil
}

const (
	gadgetRoot = "/sys/kernel/config/usb_gadget/g0"
	// a USB string descriptor holds 126 UTF-16 units; ASCII keeps it simple
	maxUSBStringLength = 126
)

var usbIDPattern = regexp.MustCompile(`^0[xX][0-9a-fA-F]{1,4}$`)

// normalizeUSBID returns the canonical 0xNNNN form, or an error.
func normalizeUSBID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !usbIDPattern.MatchString(value) {
		return "", fmt.Errorf("USB ID %q is not a hexadecimal number like 0x1234", value)
	}
	var id uint16
	if _, err := fmt.Sscanf(strings.ToLower(value), "0x%x", &id); err != nil {
		return "", fmt.Errorf("USB ID %q is not a hexadecimal number like 0x1234", value)
	}
	if id == 0 {
		return "", errors.New("USB ID 0x0000 is not a valid vendor or product ID")
	}
	return fmt.Sprintf("0x%04x", id), nil
}

func validUSBString(value string) error {
	if value == "" || len(value) > maxUSBStringLength {
		return fmt.Errorf("USB strings are 1 to %d characters", maxUSBStringLength)
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return errors.New("USB strings are printable ASCII without line breaks")
		}
	}
	if strings.TrimSpace(value) != value {
		return errors.New("USB strings do not start or end with spaces")
	}
	return nil
}

// normalized fills defaults for empty fields and puts the IDs into their
// canonical form; it does not validate.
func (i usbIdentity) normalized() usbIdentity {
	out := defaultUSBIdentity
	if v, err := normalizeUSBID(i.VendorID); err == nil {
		out.VendorID = v
	}
	if v, err := normalizeUSBID(i.ProductID); err == nil {
		out.ProductID = v
	}
	if i.Manufacturer != "" {
		out.Manufacturer = i.Manufacturer
	}
	if i.Product != "" {
		out.Product = i.Product
	}
	return out
}

func (i usbIdentity) validate() error {
	if i.VendorID != "" {
		if _, err := normalizeUSBID(i.VendorID); err != nil {
			return err
		}
	}
	if i.ProductID != "" {
		if _, err := normalizeUSBID(i.ProductID); err != nil {
			return err
		}
	}
	for _, s := range []string{i.Manufacturer, i.Product} {
		if s != "" {
			if err := validUSBString(s); err != nil {
				return err
			}
		}
	}
	return nil
}

func (i usbIdentity) isDefault() bool {
	return i.normalized() == defaultUSBIdentity
}

// files is what /boot should hold for this identity: a value for each file
// that differs from the default, nil for the files to remove.
func (i usbIdentity) files() map[string]*string {
	n := i.normalized()
	out := make(map[string]*string, 4)
	set := func(name, value, fallback string) {
		if value == fallback {
			out[name] = nil
			return
		}
		out[name] = &value
	}
	set("usb.vid", n.VendorID, defaultUSBIdentity.VendorID)
	set("usb.pid", n.ProductID, defaultUSBIdentity.ProductID)
	set("usb.manufacturer", n.Manufacturer, defaultUSBIdentity.Manufacturer)
	set("usb.product", n.Product, defaultUSBIdentity.Product)
	return out
}

// readUSBIdentity reads the identity files from bootDir. Each file stands
// on its own, as in the scripts: an unreadable or invalid one counts as the
// factory value for that field, the others keep theirs. (The scripts write
// a damaged ID to configfs as it is; the next apply rewrites the file from
// this view.)
func readUSBIdentity(bootDir string) usbIdentity {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(bootDir, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
	i := usbIdentity{VendorID: read("usb.vid"), ProductID: read("usb.pid"), Manufacturer: read("usb.manufacturer"), Product: read("usb.product")}
	if _, err := normalizeUSBID(i.VendorID); err != nil {
		i.VendorID = ""
	}
	if _, err := normalizeUSBID(i.ProductID); err != nil {
		i.ProductID = ""
	}
	if validUSBString(i.Manufacturer) != nil {
		i.Manufacturer = ""
	}
	if validUSBString(i.Product) != nil {
		i.Product = ""
	}
	return i.normalized()
}

// gadgetIdentityAt reads what the gadget presents now, from configfs.
func gadgetIdentityAt(root string) (usbIdentity, error) {
	read := func(rel string) (string, error) {
		data, err := os.ReadFile(filepath.Join(root, rel))
		return strings.TrimSpace(string(data)), err
	}
	var i usbIdentity
	var err error
	if i.VendorID, err = read("idVendor"); err != nil {
		return i, err
	}
	if i.ProductID, err = read("idProduct"); err != nil {
		return i, err
	}
	if i.Manufacturer, err = read("strings/0x409/manufacturer"); err != nil {
		return i, err
	}
	if i.Product, err = read("strings/0x409/product"); err != nil {
		return i, err
	}
	return i.normalized(), nil
}

// gadgetSerialNumberAt is the serial number string of the gadget, "" when
// the gadget is not set up.
func gadgetSerialNumberAt(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "strings/0x409/serialnumber"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
