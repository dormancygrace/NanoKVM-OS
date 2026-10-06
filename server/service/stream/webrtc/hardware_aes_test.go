package webrtc

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"NanoKVM-Server/internal/sg2002aes"
)

func TestHardwareAESRequiresExplicitOptIn(t *testing.T) {
	oldOpen, oldCapability := openHardwareAES, hardwareAESCapabilityFile
	t.Cleanup(func() {
		openHardwareAES = oldOpen
		hardwareAESCapabilityFile = oldCapability
		hardwareAESOnce = sync.Once{}
	})
	hardwareAESCapabilityFile = filepath.Join(t.TempDir(), "out_of_place_burst4")
	if err := os.WriteFile(hardwareAESCapabilityFile, []byte("Y\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var opened int
	openHardwareAES = func(func(error)) (*sg2002aes.Device, error) {
		opened++
		return nil, errors.New("test hardware unavailable")
	}

	for _, test := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default", want: false},
		{name: "software", value: "software", want: false},
		{name: "invalid", value: "unexpected", want: false},
		{name: "hardware", value: "hardware", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NANOKVM_SRTP_AES", test.value)
			hardwareAESOnce = sync.Once{}
			before := opened
			initializeHardwareAES()
			got := opened - before
			if test.want && got != 1 {
				t.Fatalf("hardware open calls=%d, want 1", got)
			}
			if !test.want && got != 0 {
				t.Fatalf("hardware open calls=%d, want 0", got)
			}
		})
	}
}

func TestHardwareAESRejectsOldLoadedModule(t *testing.T) {
	oldOpen, oldCapability := openHardwareAES, hardwareAESCapabilityFile
	t.Cleanup(func() {
		openHardwareAES = oldOpen
		hardwareAESCapabilityFile = oldCapability
		hardwareAESOnce = sync.Once{}
	})
	t.Setenv("NANOKVM_SRTP_AES", "hardware")
	hardwareAESCapabilityFile = filepath.Join(t.TempDir(), "out_of_place_burst4")
	opened := 0
	openHardwareAES = func(func(error)) (*sg2002aes.Device, error) { opened++; return nil, errors.New("test device") }
	for _, value := range []string{"missing", "N\n", "", "unknown", "Y\n"} {
		if value != "missing" {
			if err := os.WriteFile(hardwareAESCapabilityFile, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		hardwareAESOnce = sync.Once{}
		before := opened
		initializeHardwareAES()
		want := 0
		if value == "Y\n" {
			want = 1
		}
		if opened-before != want {
			t.Fatalf("module capability %q: open calls %d, want %d", value, opened-before, want)
		}
	}
}
