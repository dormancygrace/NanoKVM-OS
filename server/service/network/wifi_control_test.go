package network

import (
	gonet "net"

	"path/filepath"

	"strings"
	"testing"
)

func TestWifiProfileValidation(t *testing.T) {
	good := wifiProfile{Ssid: `Office "A"`, Password: "valid123", Band: "5", Hidden: true, Security: "personal"}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	cases := []wifiProfile{good, good, good, good, good, good, good}
	cases[0].Ssid = strings.Repeat("я", 17)
	cases[1].Ssid = "network\nkey_mgmt=NONE"
	cases[2].Password = "short"
	cases[3].Password = "valid\x00123"
	cases[4].Band = "6"
	cases[5].Security = "enterprise"
	cases[6].Security = "open"
	for i, p := range cases {
		if p.validate() == nil {
			t.Fatalf("invalid profile %d accepted", i)
		}
	}
	good.Security = "open"
	good.Password = ""
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWifiScanParsing(t *testing.T) {
	out := `BSS 00:11:22:33:44:01(on wlan0)
 freq: 5180.0
 signal: -61.00 dBm
 capability: ESS Privacy
 SSID: Office\x20WiFi
 RSN:
 * Authentication suites: PSK
BSS 00:11:22:33:44:02(on wlan0)
 freq: 5200
 signal: -42.00 dBm
 capability: ESS Privacy
 SSID: Office WiFi
 RSN:
 * Authentication suites: PSK
BSS 00:11:22:33:44:03(on wlan0)
 freq: 2412
 signal: -30.00 dBm
 SSID: Wrong band
BSS 00:11:22:33:44:04(on wlan0)
 freq: 5180.0
 signal: -70.00 dBm
 SSID: Cafe\x20
BSS 00:11:22:33:44:05(on wlan0)
 freq: 5180.0
 signal: -80.00 dBm
 capability: ESS Privacy
 SSID: Enterprise
 RSN:
 * Authentication suites: IEEE 802.1X
BSS 00:11:22:33:44:06(on wlan0)
 freq: 5180.0
 SSID:
BSS 00:11:22:33:44:07(on wlan0)
 freq: 5180.0
 signal: -85.00 dBm
 capability: ESS Privacy
 SSID: WEP
BSS 00:11:22:33:44:08(on wlan0)
 freq: 5180.0
 signal: -90.00 dBm
 SSID: Enhanced open
 RSN:
 * Authentication suites: OWE
`
	got := parseScan(out, "5")
	if len(got) != 5 {
		t.Fatalf("%+v", got)
	}
	if got[0].Ssid != "Office WiFi" || got[0].Signal != -42 || got[0].Security != "wpa2" {
		t.Fatal(got[0])
	}
	if got[1].Ssid != "Cafe " || got[1].Security != "open" {
		t.Fatal(got[1])
	}
	for _, n := range got[2:] {
		if n.Security != "unsupported" {
			t.Fatal(n)
		}
	}
}

// Issue 215: a board without Wi-Fi must not report Wi-Fi as on.

func TestWpaControlRequest(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wlan0")
	server, err := gonet.ListenUnixgram("unixgram", &gonet.UnixAddr{Net: "unixgram", Name: socket})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		buf := make([]byte, 64)
		n, from, err := server.ReadFromUnix(buf)
		if err != nil || from == nil {
			return
		}
		// An event before the reply must be skipped.
		_, _ = server.WriteToUnix([]byte("<3>CTRL-EVENT-SCAN-STARTED"), from)
		_, _ = server.WriteToUnix([]byte("wpa_state=COMPLETED\nrequest="+string(buf[:n])+"\n"), from)
	}()
	reply, err := wpaControl(socket)("STATUS")
	if err != nil || reply != "wpa_state=COMPLETED\nrequest=STATUS\n" {
		t.Fatalf("reply %q, err %v", reply, err)
	}
	if _, err = wpaControl(filepath.Join(t.TempDir(), "absent"))("STATUS"); err == nil {
		t.Fatal("missing wpa_supplicant socket reported a reply")
	}
}
