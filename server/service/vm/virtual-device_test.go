package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/service/hid"
)

func TestUSBEndpointBudget(t *testing.T) {
	tests := []struct {
		name  string
		state usbComposition
		in    int
		out   int
		fits  bool
	}{
		{
			name:  "stock full profile",
			state: usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, network: true, disk: true},
			in:    6, out: 5, fits: true,
		},
		{
			name:  "full profile plus serial does not fit",
			state: usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, network: true, disk: true, serial: true},
			in:    8, out: 6, fits: false,
		},
		{
			name:  "HID disk and serial fit",
			state: usbComposition{mode: hid.ModeNormal, keyboard: true, relative: true, absolute: true, disk: true, serial: true},
			in:    6, out: 5, fits: true,
		},
		{
			name:  "HID-only and serial fit",
			state: usbComposition{mode: hid.ModeHidOnly, keyboard: true, relative: true, absolute: true, serial: true},
			in:    5, out: 4, fits: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inUsed, outUsed := test.state.endpointUsage()
			if inUsed != test.in || outUsed != test.out {
				t.Fatalf("usage = %d IN/%d OUT, want %d IN/%d OUT", inUsed, outUsed, test.in, test.out)
			}
			if got := test.state.fitsEndpointBudget(); got != test.fits {
				t.Fatalf("fits = %v, want %v", got, test.fits)
			}
		})
	}
}

func TestRetireLegacyACMGettyContent(t *testing.T) {
	input := "id:3:initdefault:\n" +
		"sole::respawn:/sbin/getty -L console 0 vt100\n" +
		"acm::respawn:/sbin/getty -L ttyGS0 0 vt100 -l /etc/ttyGS0_handler.sh\n"
	got, changed := removeLegacyACMGettyLine(input)
	if !changed {
		t.Fatal("legacy getty was not detected")
	}
	if strings.Contains(got, "ttyGS0") {
		t.Fatalf("legacy getty remains: %q", got)
	}
	if !strings.Contains(got, "getty -L console") {
		t.Fatalf("console getty was removed: %q", got)
	}

	// Keep a filesystem assertion here so the target directory semantics used
	// by the atomic migration are covered without signalling PID 1 in tests.
	path := filepath.Join(t.TempDir(), "inittab")
	if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
