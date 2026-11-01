package srtpaccel

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"NanoKVM-Server/internal/hwcrypto"
)

func TestHardwareAESRequiresExplicitOptIn(t *testing.T) {
	oldOpen, oldCapability := openHardwareAES, hardwareAESCapabilityFiles
	t.Cleanup(func() {
		openHardwareAES = oldOpen
		hardwareAESCapabilityFiles = oldCapability
		hardwareAESOnce = sync.Once{}
	})
	capability := filepath.Join(t.TempDir(), "out_of_place_burst4")
	hardwareAESCapabilityFiles = []string{capability}
	if err := os.WriteFile(capability, []byte("Y\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var opened int
	openHardwareAES = func(func(error)) (*hwcrypto.Device, error) {
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
