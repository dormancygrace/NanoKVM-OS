package remotemedia

import (
	"strings"
	"testing"
)

func TestSMBOptions(t *testing.T) {
	for _, raw := range []string{"smb://host/share/a.iso", "smb://[::1]:1445/share/directory/a%20b.ISO"} {
		if err := ValidateSMBOptions(SMBOptions{URL: raw, Username: "reader", Domain: "WORKGROUP"}); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"http://host/a.iso", "smb://host/share", "smb://host/share/a.img", "smb://user:secret@host/share/a.iso", "smb://host/share/a.iso?secret", "smb://host/share/a.iso#fragment", "smb://host:0/share/a.iso", "smb://host:65536/share/a.iso", "smb:///share/a.iso", "smb://host/share/../a.iso", "smb://host/share/%2e%2e/a.iso", "smb://host/share/a%5cb.iso", "smb://host/share/a.iso:stream", "smb://host/share//a.iso", "smb://host/share/a%00.iso", "smb://host/share/a.iso."} {
		if err := ValidateSMBOptions(SMBOptions{URL: raw, Username: "reader"}); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, user := range []string{"", "domain\\user", "bad\nuser", strings.Repeat("u", 257)} {
		if ValidateSMBOptions(SMBOptions{URL: "smb://host/share/a.iso", Username: user}) == nil {
			t.Errorf("accepted username %q", user)
		}
	}
}

// Run against a real signed Samba fixture, including an encryption-required
// share, with NANOKVM_SMB_TEST_ADDR=127.0.0.1:3445. Requires root/CAP_SYS_ADMIN and a kernel with CIFS and the new mount API.
