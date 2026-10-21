package vm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/proto"
)

func TestNormalizeUSBID(t *testing.T) {
	for in, want := range map[string]string{"0x3346": "0x3346", "0X1009": "0x1009", " 0x46d ": "0x046d", "0xFFFF": "0xffff", "0x1": "0x0001"} {
		got, err := normalizeUSBID(in)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "3346", "0x", "0x12345", "0x0000", "0xzz", "4660"} {
		if _, err := normalizeUSBID(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func TestUSBIdentityValidateAndDefaults(t *testing.T) {
	if err := (usbIdentity{}).validate(); err != nil {
		t.Fatal(err)
	}
	if !(usbIdentity{}).isDefault() || !(usbIdentity{VendorID: "0X3346", Product: "NanoKVM"}).isDefault() {
		t.Fatal("the zero identity is the default")
	}
	ok := usbIdentity{VendorID: "0x046d", ProductID: "0xc31c", Manufacturer: "Generic", Product: "USB Composite Device"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	if ok.isDefault() {
		t.Fatal("a custom identity is not the default")
	}
	serial := usbIdentity{SerialNumber: "KVM_01.a-B"}
	if err := serial.validate(); err != nil || serial.isDefault() {
		t.Fatalf("serial number: %v", err)
	}
	bad := []usbIdentity{
		{VendorID: "046d"},
		{ProductID: "0x0000"},
		{Manufacturer: strings.Repeat("x", 127)},
		{Product: "line\nbreak"},
		{Product: "ünïcode"},
		{Manufacturer: " padded"},
		{SerialNumber: "has space"},
		{SerialNumber: `quo"te`},
		{SerialNumber: "$(id)"},
		{SerialNumber: strings.Repeat("x", 127)},
	}
	for _, i := range bad {
		if err := i.validate(); err == nil {
			t.Fatalf("%+v accepted", i)
		}
	}
}

func TestUSBIdentityFilesAndRead(t *testing.T) {
	files := (usbIdentity{}).files()
	for name, content := range files {
		if content != nil {
			t.Fatalf("default identity writes %s", name)
		}
	}
	custom := usbIdentity{VendorID: "0X46D", Product: "USB Composite Device"}
	files = custom.files()
	if files["usb.vid"] == nil || *files["usb.vid"] != "0x046d" || files["usb.pid"] != nil ||
		files["usb.manufacturer"] != nil || files["usb.product"] == nil || *files["usb.product"] != "USB Composite Device" {
		t.Fatalf("files %v", files)
	}

	dir := t.TempDir()
	if got := readUSBIdentity(dir); got != defaultUSBIdentity {
		t.Fatalf("empty boot dir: %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "usb.vid"), []byte("0x046d\n"), 0644)
	os.WriteFile(filepath.Join(dir, "usb.product"), []byte("USB Composite Device\n"), 0644)
	if got := readUSBIdentity(dir); got != (usbIdentity{VendorID: "0x046d", ProductID: "0x1009", Manufacturer: "sipeed", Product: "USB Composite Device"}) {
		t.Fatalf("read %+v", got)
	}
	// a damaged file is the factory value for that field alone
	os.WriteFile(filepath.Join(dir, "usb.vid"), []byte("garbage\n"), 0644)
	if got := readUSBIdentity(dir); got != (usbIdentity{VendorID: "0x3346", ProductID: "0x1009", Manufacturer: "sipeed", Product: "USB Composite Device"}) {
		t.Fatalf("damaged file: %+v", got)
	}

	// the serial number file is written only when one is set
	files = usbIdentity{SerialNumber: "KVM-0001"}.files()
	if files["usb.serialnumber"] == nil || *files["usb.serialnumber"] != "KVM-0001" || files["usb.vid"] != nil {
		t.Fatalf("serial files %v", files)
	}
	if _, ok := (usbIdentity{}).files()["usb.serialnumber"]; !ok {
		t.Fatal("the default identity does not remove usb.serialnumber")
	}
	os.WriteFile(filepath.Join(dir, "usb.serialnumber"), []byte("KVM-0001\n"), 0644)
	if got := readUSBIdentity(dir); got.SerialNumber != "KVM-0001" {
		t.Fatalf("serial read %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "usb.serialnumber"), []byte("bad serial\n"), 0644)
	if got := readUSBIdentity(dir); got.SerialNumber != "" {
		t.Fatalf("damaged serial %+v", got)
	}
}

func TestChipSerialNumber(t *testing.T) {
	old := chipUIDPath
	defer func() { chipUIDPath = old }()
	chipUIDPath = filepath.Join(t.TempDir(), "base_uid")
	if got := chipSerialNumber(); got != fallbackSerialNumber {
		t.Fatalf("missing: %q", got)
	}
	os.WriteFile(chipUIDPath, []byte("UID: d19506b3_1359c372\n"), 0644)
	if got := chipSerialNumber(); got != "d19506b31359c372" {
		t.Fatalf("uid: %q", got)
	}
	os.WriteFile(chipUIDPath, []byte("UID: 1234_5\n"), 0644)
	if got := chipSerialNumber(); got != fallbackSerialNumber {
		t.Fatalf("short: %q", got)
	}
}

// What the handler does with the request's identity: a bad value is an
// error, not quietly the factory one; none means "keep"; a good one is
// normalised.
func TestIdentityFromRequest(t *testing.T) {
	if got, err := identityFromRequest(nil); err != nil || got != (usbIdentity{}) {
		t.Fatalf("nil: %+v %v", got, err)
	}
	if _, err := identityFromRequest(&proto.USBIdentityReq{VendorID: "046d"}); err == nil {
		t.Fatal("046d was taken as the factory VID")
	}
	if _, err := identityFromRequest(&proto.USBIdentityReq{Product: "line\nbreak"}); err == nil {
		t.Fatal("a line break was accepted")
	}
	if _, err := identityFromRequest(&proto.USBIdentityReq{SerialNumber: "a b"}); err == nil {
		t.Fatal("a serial number with a space was accepted")
	}
	got, err := identityFromRequest(&proto.USBIdentityReq{VendorID: "0X46D", Product: "USB Composite Device", SerialNumber: "KVM-0001"})
	if err != nil || got != (usbIdentity{VendorID: "0x046d", ProductID: "0x1009", Manufacturer: "sipeed", Product: "USB Composite Device", SerialNumber: "KVM-0001"}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func writeGadgetIdentity(t *testing.T, root string, i usbIdentity) {
	t.Helper()
	os.MkdirAll(filepath.Join(root, "strings/0x409"), 0755)
	for rel, value := range map[string]string{"idVendor": i.VendorID, "idProduct": i.ProductID,
		"strings/0x409/manufacturer": i.Manufacturer, "strings/0x409/product": i.Product, "strings/0x409/serialnumber": "d19506b31359c372"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(value+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVerifyChecksTheGadgetIdentity(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "configs/c.1"), 0755)
	os.WriteFile(filepath.Join(root, "UDC"), []byte("4340000.usb\n"), 0644)
	os.Symlink("../../functions/hid.GS0", filepath.Join(root, "configs/c.1/hid.GS0"))
	s := usbComposition{mode: "normal", keyboard: true}
	writeGadgetIdentity(t, root, defaultUSBIdentity)
	if err := verifyUSBCompositionAt(root, s); err != nil {
		t.Fatal(err)
	}
	s.identity = usbIdentity{VendorID: "0x046d", Product: "USB Composite Device"}
	if err := verifyUSBCompositionAt(root, s); err == nil {
		t.Fatal("the old identity passed")
	}
	writeGadgetIdentity(t, root, usbIdentity{VendorID: "0x046D", ProductID: "0x1009", Manufacturer: "sipeed", Product: "USB Composite Device"})
	if err := verifyUSBCompositionAt(root, s); err != nil {
		t.Fatal(err)
	}
	if gadgetSerialNumberAt(root) != "d19506b31359c372" {
		t.Fatal("serial number")
	}
	// a serial number that is set is checked; the chip UID is not
	s.identity.SerialNumber = "KVM-0001"
	if err := verifyUSBCompositionAt(root, s); err == nil {
		t.Fatal("the chip UID passed for a set serial number")
	}
	os.WriteFile(filepath.Join(root, "strings/0x409/serialnumber"), []byte("KVM-0001\n"), 0644)
	if err := verifyUSBCompositionAt(root, s); err != nil {
		t.Fatal(err)
	}
}

func TestHIDOnlySerialOverrideCanBeCleared(t *testing.T) {
	data, err := os.ReadFile("../../../kvmapp/system/init.d/S03usbhid")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	start := strings.Index(script, "    if [ -e /boot/usb.acm ] ||")
	end := strings.Index(script, "    # The identity files")
	if start < 0 || end < start {
		t.Fatal("serial initialization block not found")
	}
	dir := t.TempDir()
	for _, name := range []string{"boot", "strings/0x409"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	block := strings.ReplaceAll(script[start:end], "/boot/", dir+"/boot/")
	block = strings.ReplaceAll(block, "/sys/class/cvi-base/base_uid", dir+"/base_uid")
	serialFile := filepath.Join(dir, "boot/usb.serialnumber")
	if err := os.WriteFile(serialFile, []byte("KVM-0001"), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(want string) {
		t.Helper()
		cmd := exec.Command("sh", "-ec", block)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, output)
		}
		got, err := os.ReadFile(filepath.Join(dir, "strings/0x409/serialnumber"))
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Fatalf("serial=%q want=%q err=%v", got, want, err)
		}
	}
	run("KVM-0001")
	if err := os.Remove(serialFile); err != nil {
		t.Fatal(err)
	}
	run("")
	// ACM still gets the chip UID when the override is cleared.
	if err := os.WriteFile(filepath.Join(dir, "boot/usb.acm"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "base_uid"), []byte("UID: d19506b3_1359c372"), 0644); err != nil {
		t.Fatal(err)
	}
	run("d19506b31359c372")
}
