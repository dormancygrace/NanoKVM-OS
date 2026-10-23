package download

import (
	"os"
	"testing"

	"NanoKVM-Server/service/hid"
)

// Image installs check the USB boot markers in hid.USBFlagsDir. The tests
// must not read the flags of the machine that runs them.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "download-usb-flags-")
	if err != nil {
		panic(err)
	}
	hid.USBFlagsDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
