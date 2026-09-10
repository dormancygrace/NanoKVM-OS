package hid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisabledHIDFunctions(t *testing.T) {
	root := t.TempDir()
	if k, r, a := disabledHIDFunctions(root); k || r || a {
		t.Fatal("default functions disabled")
	}
	if err := os.WriteFile(filepath.Join(root, "usb.disable_relative"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if k, r, a := disabledHIDFunctions(root); k || !r || a {
		t.Fatal("individual flag disables wrong function")
	}
	if err := os.WriteFile(filepath.Join(root, "disable_hid"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if k, r, a := disabledHIDFunctions(root); !k || !r || !a {
		t.Fatal("legacy master flag ignored")
	}
}

func TestDisabledHIDReportsDoNotOpenDevices(t *testing.T) {
	h := &Hid{keyboardDisabled: true, relativeDisabled: true, absoluteDisabled: true}
	if err := h.WriteKeyboardReport(make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteRelativeMouseReport(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	if err := h.WriteAbsoluteMouseReport(make([]byte, 6)); err != nil {
		t.Fatal(err)
	}
	if h.g0 != nil || h.g1 != nil || h.g2 != nil {
		t.Fatal("disabled device was opened")
	}
}
