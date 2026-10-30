package logs

import (
	"strings"
	"testing"
)

func TestMaskAddresses(t *testing.T) {
	in := strings.Join([]string{
		"inet 192.168.4.128/24 brd 192.168.4.255 scope global eth0",
		"default via 10.0.0.1 dev eth0 metric 202",
		"inet 127.0.0.1/8 scope host lo",
		"netmask 255.255.255.0 any 0.0.0.0",
		"link/ether 48:da:35:6f:1a:2b brd ff:ff:ff:ff:ff:ff",
		"inet6 fe80::4ada:35ff:fe6f:1a2b/64 scope link",
		"inet6 2001:db8:1::42/64 scope global",
		"inet6 2001:470:20:23:0:0:0:50/64 scope global",
		"inet6 ::1/128 scope host",
		"Oct 30 15:04:05 nanokvm kernel: [12:34:56] up",
		"nanokvm-base-3.6.0-r0 kernel 7.3.0-nanokvm-os-r3 Alpine 3.24.3",
		"[hdmi] diagnostics ready fw=0x00000300 model=LT6911UXC chip_id=0x0417",
		"monitor_edid_sha256: 3f2a9c0d4e5b6a7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7",
		"\tSSID: HomeNetwork",
		"\t\tssid Upstairs Mesh Net",
		"ssid=\"Office Wi-Fi\"",
		"Oct 30 12:34:56 nanokvm wpa_supplicant: wlan0: Trying to associate with SSID 'SyntheticHome595'",
		"wlan0: CTRL-EVENT-SSID-TEMP-DISABLED id=0 ssid=\"Cafe Net\" auth_failures=1",
		"scan result ssid=GuestNet freq=2412",
	}, "\n")
	out := MaskAddresses(in)
	for _, leaked := range []string{"192.168.4.128", "192.168.4.255", "10.0.0.1", "48:da:35", "fe80::4ada", "2001:db8", "2001:470:20:23", "HomeNetwork", "Upstairs", "Mesh Net", "Office Wi-Fi", "SyntheticHome595", "Cafe Net", "GuestNet"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%q not masked:\n%s", leaked, out)
		}
	}
	for _, kept := range []string{"monitor_edid_sha256: 3f2a9c0d", "127.0.0.1/8", "255.255.255.0", "0.0.0.0", "::1/128", "15:04:05", "12:34:56", "3.6.0-r0", "7.3.0-nanokvm-os-r3", "3.24.3", "fw=0x00000300", "chip_id=0x0417", "auth_failures=1", "freq=2412"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q lost:\n%s", kept, out)
		}
	}
}

func TestSanitizeFileHidesSecretsAndMasks(t *testing.T) {
	in := "proto: https\nauthentication:\n  jwt:\n    secretKey: hunter2hunter2\nturn:\n    api_key: zzz\nhost 192.168.1.20\npsk=\"wifi-pass\"\nlast line without newline 10.1.2.3"
	out := SanitizeFile([]byte(in))
	for _, leaked := range []string{"hunter2", "wifi-pass", "zzz", "192.168.1.20", "10.1.2.3"} {
		if strings.Contains(out, leaked) {
			t.Errorf("%q leaked:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, "proto: https") || !strings.Contains(out, "last line without newline <ip>") {
		t.Errorf("content lost:\n%s", out)
	}
}
