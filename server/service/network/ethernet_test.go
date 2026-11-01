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
	for _, mask := range []string{"", "255.255.255.0"} {
		prefix, gateway, err = validateStaticEthernet(" 192.168.10.32/24 ", mask, "192.168.10.1")
		if err != nil || prefix.String() != "192.168.10.32/24" || gateway.String() != "192.168.10.1" {
			t.Fatalf("CIDR with mask %q: %s %s %v", mask, prefix, gateway, err)
		}
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
		{"CIDR zero prefix", "192.168.10.32/0", "", ""},
		{"CIDR point to point prefix", "192.168.10.32/31", "", ""},
		{"CIDR host prefix", "192.168.10.32/32", "", ""},
		{"invalid CIDR prefix", "192.168.10.32/33", "", "192.168.10.1"},
		{"CIDR network address", "192.168.10.0/24", "", "192.168.10.1"},
		{"CIDR with a different mask", "192.168.10.32/24", "255.255.0.0", "192.168.10.1"},
		{"CIDR gateway outside", "192.168.10.32/24", "", "192.168.11.1"},
		{"IPv6 CIDR", "fd00::5/64", "", "192.168.10.1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := validateStaticEthernet(test.address, test.mask, test.gateway); err == nil {
				t.Fatal("expected validation error")
			}
		})
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

func TestWriteEthernetConfigRejectsUnsupportedCIDR(t *testing.T) {
	original := ethernetConfigFile
	ethernetConfigFile = filepath.Join(t.TempDir(), "eth.nodhcp")
	t.Cleanup(func() { ethernetConfigFile = original })
	const previous = "192.168.10.32/24 none\n"
	if err := os.WriteFile(ethernetConfigFile, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"192.168.10.32/0", "192.168.10.32/31", "192.168.10.32/32"} {
		if err := writeEthernetConfig(proto.EthernetConfig{Mode: ethernetModeStatic, Address: address}); err == nil {
			t.Fatalf("accepted unsupported CIDR %s", address)
		}
		got, err := os.ReadFile(ethernetConfigFile)
		if err != nil || string(got) != previous {
			t.Fatalf("invalid request changed saved config: %q, %v", got, err)
		}
	}
	for _, address := range []string{"10.0.0.5/1", "10.0.0.5/30"} {
		if _, gateway, err := validateStaticEthernet(address, "", ""); err != nil || gateway.IsValid() {
			t.Fatalf("valid boundary %s without gateway: %v, %v", address, gateway, err)
		}
	}
}
