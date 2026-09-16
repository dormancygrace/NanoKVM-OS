package network

import (
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/proto"
)

func TestValidateStaticEthernet(t *testing.T) {
	prefix, gateway, err := validateStaticEthernet("192.168.10.32", "255.255.255.0", "192.168.10.1")
	if err != nil {
		t.Fatalf("expected valid configuration, got %v", err)
	}
	if prefix.String() != "192.168.10.32/24" || gateway.String() != "192.168.10.1" {
		t.Fatalf("unexpected normalized configuration: %s %s", prefix, gateway)
	}

	for _, test := range []struct {
		name, address, mask, gateway string
	}{
		{"invalid address", "192.168.10.999", "255.255.255.0", "192.168.10.1"},
		{"network address", "192.168.10.0", "255.255.255.0", "192.168.10.1"},
		{"broadcast address", "192.168.10.255", "255.255.255.0", "192.168.10.1"},
		{"noncontiguous mask", "192.168.10.32", "255.0.255.0", "192.168.10.1"},
		{"unsupported mask", "192.168.10.32", "255.255.255.255", "192.168.10.1"},
		{"different subnet gateway", "192.168.10.32", "255.255.255.0", "192.168.11.1"},
		{"same address gateway", "192.168.10.32", "255.255.255.0", "192.168.10.32"},
		{"broadcast gateway", "192.168.10.32", "255.255.255.0", "192.168.10.255"},
		{"broadcast gateway small subnet", "10.0.0.5", "255.255.255.252", "10.0.0.7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateStaticEthernet(test.address, test.mask, test.gateway); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestEthernetConfigPersistence(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := ethernetConfigFile
	originalDisabledFile := ethernetDisabledFile
	originalVLANFile := ethernetVLANFile
	originalCarrierFile := ethernetCarrierFile
	originalFlagsFile := ethernetFlagsFile
	ethernetConfigFile = filepath.Join(tempDir, "boot", "eth.nodhcp")
	ethernetDisabledFile = filepath.Join(tempDir, "boot", "eth.disabled")
	ethernetVLANFile = filepath.Join(tempDir, "boot", "eth.vlan")
	ethernetCarrierFile = filepath.Join(tempDir, "carrier")
	ethernetFlagsFile = filepath.Join(tempDir, "flags")
	if err := os.WriteFile(ethernetCarrierFile, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ethernetFlagsFile, []byte("0x1003\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ethernetConfigFile = originalFile
		ethernetDisabledFile = originalDisabledFile
		ethernetVLANFile = originalVLANFile
		ethernetCarrierFile = originalCarrierFile
		ethernetFlagsFile = originalFlagsFile
	})

	static := proto.EthernetConfig{
		Mode:       ethernetModeStatic,
		Interface:  ethernetInterface,
		Address:    "10.0.0.20",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1",
	}
	if err := writeEthernetConfig(static); err != nil {
		t.Fatalf("write static config: %v", err)
	}
	if err := writeEthernetVLAN(true, 123); err != nil {
		t.Fatalf("write VLAN config: %v", err)
	}

	got, err := readEthernetConfig()
	if err != nil {
		t.Fatalf("read static config: %v", err)
	}
	if !got.Enabled || !got.AdminUp || !got.LinkUp || got.Mode != ethernetModeStatic || got.Address != static.Address || got.Gateway != static.Gateway || got.SubnetMask != static.SubnetMask || !got.VLANEnabled || got.VLANID != 123 {
		t.Fatalf("unexpected static config: %#v", got)
	}

	if err := writeEthernetEnabled(false); err != nil {
		t.Fatalf("disable ethernet: %v", err)
	}
	got, err = readEthernetConfig()
	if err != nil {
		t.Fatalf("read disabled config: %v", err)
	}
	if got.Enabled || !got.AdminUp || !got.LinkUp || got.Mode != ethernetModeStatic {
		t.Fatalf("disabled preference and physical carrier must be independent: %#v", got)
	}
	info, err := os.Stat(ethernetDisabledFile)
	if err != nil {
		t.Fatalf("stat disabled marker: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("disabled marker permissions = %o, want 600", info.Mode().Perm())
	}
	if err := writeEthernetEnabled(true); err != nil {
		t.Fatalf("enable ethernet: %v", err)
	}
	if _, err := os.Stat(ethernetDisabledFile); !os.IsNotExist(err) {
		t.Fatalf("enabled state should remove disabled marker, stat error: %v", err)
	}

	if err := writeEthernetConfig(proto.EthernetConfig{Mode: ethernetModeDHCP}); err != nil {
		t.Fatalf("enable dhcp: %v", err)
	}
	if err := writeEthernetVLAN(false, 0); err != nil {
		t.Fatalf("disable VLAN: %v", err)
	}
	if _, err := os.Stat(ethernetConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dhcp mode should remove static config, stat error: %v", err)
	}

	got, err = readEthernetConfig()
	if err != nil {
		t.Fatalf("read dhcp config: %v", err)
	}
	if !got.Enabled || !got.AdminUp || !got.LinkUp || got.Mode != ethernetModeDHCP || got.Interface != ethernetInterface || got.VLANEnabled || got.VLANID != 0 {
		t.Fatalf("unexpected dhcp config: %#v", got)
	}
}

func TestReadEthernetCarrierFailsClosed(t *testing.T) {
	originalCarrierFile := ethernetCarrierFile
	ethernetCarrierFile = filepath.Join(t.TempDir(), "missing-carrier")
	t.Cleanup(func() { ethernetCarrierFile = originalCarrierFile })

	if readEthernetCarrier() {
		t.Fatal("missing carrier state must not be reported as a connected cable")
	}
}

func TestReadEthernetAdminUp(t *testing.T) {
	originalFlagsFile := ethernetFlagsFile
	ethernetFlagsFile = filepath.Join(t.TempDir(), "flags")
	t.Cleanup(func() { ethernetFlagsFile = originalFlagsFile })

	if readEthernetAdminUp() {
		t.Fatal("missing flags must not be reported as administratively up")
	}
	for _, test := range []struct {
		flags string
		want  bool
	}{
		{"0x1002\n", false},
		{"0x1003\n", true},
		{"invalid\n", false},
	} {
		if err := os.WriteFile(ethernetFlagsFile, []byte(test.flags), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readEthernetAdminUp(); got != test.want {
			t.Errorf("flags %q: adminUp=%t, want %t", test.flags, got, test.want)
		}
	}
}

func TestReadEthernetConfigIgnoresCommentsAndCRLF(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := ethernetConfigFile
	ethernetConfigFile = filepath.Join(tempDir, "eth.nodhcp")
	t.Cleanup(func() { ethernetConfigFile = originalFile })

	contents := "# NanoKVM static network\r\n\r\n  10.20.30.40/24 10.20.30.1  # gateway\r\n"
	if err := os.WriteFile(ethernetConfigFile, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readEthernetConfig()
	if err != nil {
		t.Fatalf("read commented config: %v", err)
	}
	if got.Mode != ethernetModeStatic || got.Address != "10.20.30.40" || got.SubnetMask != "255.255.255.0" || got.Gateway != "10.20.30.1" {
		t.Fatalf("unexpected commented config: %#v", got)
	}
}

func TestReadEthernetConfigRejectsExtraFieldsAfterCommentRemoval(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := ethernetConfigFile
	ethernetConfigFile = filepath.Join(tempDir, "eth.nodhcp")
	t.Cleanup(func() { ethernetConfigFile = originalFile })

	if err := os.WriteFile(ethernetConfigFile, []byte("10.20.30.40/24 10.20.30.1 unexpected # comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEthernetConfig(); err == nil {
		t.Fatal("expected extra field to be rejected")
	}
}
