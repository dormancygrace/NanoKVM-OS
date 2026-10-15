package network

import (
	"os"
	"path/filepath"
	"testing"

	"NanoKVM-Server/proto"
)

func TestStaticEthernetWithoutGateway(t *testing.T) {
	prefix, gateway, err := validateStaticEthernet("192.168.1.20/24", "", " ")
	if err != nil || prefix.String() != "192.168.1.20/24" || gateway.IsValid() {
		t.Fatalf("no gateway: %s %s %v", prefix, gateway, err)
	}

	tempDir := t.TempDir()
	original := ethernetConfigFile
	ethernetConfigFile = filepath.Join(tempDir, "eth.nodhcp")
	t.Cleanup(func() { ethernetConfigFile = original })
	if err := writeEthernetConfig(proto.EthernetConfig{Mode: ethernetModeStatic, Address: "192.168.1.20/24"}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(ethernetConfigFile); string(data) != "192.168.1.20/24 none\n" {
		t.Fatalf("written %q", data)
	}
	got, err := readEthernetConfig()
	if err != nil || got.Mode != ethernetModeStatic || got.Address != "192.168.1.20" || got.SubnetMask != "255.255.255.0" || got.Gateway != "" {
		t.Fatalf("read back %#v, %v", got, err)
	}

	// The Sipeed format without a second field still means network+1.
	if err := os.WriteFile(ethernetConfigFile, []byte("10.0.0.5/24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err = readEthernetConfig(); err != nil || got.Gateway != "10.0.0.1" {
		t.Fatalf("one-field file: %#v, %v", got, err)
	}
}
