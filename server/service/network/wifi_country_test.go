package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"NanoKVM-Server/proto"
)

// The country reaches iw's argv and the hostapd and wpa_supplicant files.
func TestWifiCountryValidation(t *testing.T) {
	for _, code := range []string{"00", "DE", "US", "JP", "GB"} {
		if !validWifiCountry(code) {
			t.Errorf("%q rejected", code)
		}
	}
	for _, code := range []string{"", "de", "D", "DEU", "0", "01", "99", "98", "D1", "DE\n", "D E", "-h", "É", "\x00\x00"} {
		if validWifiCountry(code) {
			t.Errorf("%q accepted", code)
		}
	}
}

func TestWifiCountryStorage(t *testing.T) {
	w := radioControl{etc: t.TempDir()}
	if got := w.country(); got != "00" {
		t.Fatalf("missing file: %q", got)
	}
	if err := w.saveCountry("de"); err == nil {
		t.Fatal("lowercase country saved")
	}
	if err := w.saveCountry("DE"); err != nil {
		t.Fatal(err)
	}
	if got := w.country(); got != "DE" {
		t.Fatalf("saved: %q", got)
	}
	// A file S30wifi would not take is the world domain here too.
	for _, data := range []string{"de\n", "DEU\n", "D\n", "\n"} {
		if err := os.WriteFile(filepath.Join(w.etc, wifiCountryFile), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := w.country(); got != "00" {
			t.Fatalf("%q read as %q", data, got)
		}
	}
	if err := os.WriteFile(filepath.Join(w.etc, wifiCountryFile), []byte("FR\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := w.country(); got != "FR" {
		t.Fatalf("CRLF: %q", got)
	}
}

// iw 5.x and later: the global domain first, then self-managed wiphys.
const regGetGlobal = `global
country DE: DFS-ETSI
	(2400 - 2483 @ 40), (N/A, 20), (N/A)
	(5150 - 5250 @ 80), (N/A, 23), (N/A), NO-OUTDOOR, AUTO-BW
	(5250 - 5350 @ 80), (N/A, 20), (0 ms), NO-OUTDOOR, DFS, AUTO-BW
	(5470 - 5725 @ 160), (N/A, 26), (0 ms), DFS
	(5725 - 5875 @ 80), (N/A, 13), (N/A)

phy#0 (self-managed)
country US: DFS-FCC
	(2402 - 2472 @ 40), (N/A, 30), (N/A)
`

const regGetWorld = `global
country 00: DFS-UNSET
	(755 - 928 @ 2), (N/A, 20), (N/A), NO-IR
	(2402 - 2472 @ 40), (N/A, 20), (N/A)
	(2457 - 2482 @ 20), (N/A, 20), (N/A), AUTO-BW, NO-IR
	(2474 - 2494 @ 20), (N/A, 20), (N/A), NO-OFDM, NO-IR
	(5170 - 5250 @ 80), (N/A, 20), (N/A), AUTO-BW, NO-IR
`

func TestParseRegCountry(t *testing.T) {
	cases := map[string]string{
		regGetGlobal: "DE",
		regGetWorld:  "00",
		// iw before sections.
		"country JP: DFS-JP\n\t(2402 - 2482 @ 40), (N/A, 20), (N/A)\n": "JP",
		// Only a self-managed wiphy's domain: not the global one.
		"global\nphy#0 (self-managed)\ncountry US: DFS-FCC\n": "",
		"country 98: DFS-UNSET\n":                             "98",
		"command failed: No such file or directory (-2)\n":    "",
		"": "",
	}
	for out, want := range cases {
		if got := parseRegCountry(out); got != want {
			t.Errorf("%q: got %q, want %q", out, got, want)
		}
	}
}

// iw phy phy0 info of an AIC8800 in DE (excerpt of its two bands).
const phyInfoDE = `Wiphy phy0
	Band 1:
		Frequencies:
			* 2412.0 MHz [1] (20.0 dBm)
			* 2417.0 MHz [2] (20.0 dBm)
			* 2462.0 MHz [11] (20.0 dBm)
			* 2467.0 MHz [12] (20.0 dBm)
			* 2472.0 MHz [13] (20.0 dBm)
			* 2484.0 MHz [14] (disabled)
	Band 2:
		Frequencies:
			* 5180.0 MHz [36] (20.0 dBm)
			* 5240.0 MHz [48] (20.0 dBm)
			* 5260.0 MHz [52] (20.0 dBm) (radar detection)
			* 5500.0 MHz [100] (26.0 dBm) (no IR, radar detection)
			* 5745.0 MHz [149] (13.0 dBm)
			* 5825.0 MHz [165] (disabled)
			* 5845.0 MHz [169] (no IR)
`

func TestParseAPChannels(t *testing.T) {
	want := map[string][]int{"2.4": {1, 2, 11, 12, 13}, "5": {36, 48, 149}}
	if got := parseAPChannels(phyInfoDE); !reflect.DeepEqual(got, want) {
		t.Fatalf("DE: %v", got)
	}
	// The world domain: 12 and 13 and all of 5 GHz without IR; older iw
	// said "passive scanning" and "no IBSS". 14 (802.11b only) and 4.9 GHz
	// never.
	world := "* 2412 MHz [1] (20.0 dBm)\n* 2467 MHz [12] (20.0 dBm) (no IR)\n" +
		"* 2472 MHz [13] (20.0 dBm) (passive scanning, no IBSS)\n* 5180 MHz [36] (20.0 dBm) (no IR)\n" +
		"* 4920 MHz [184] (20.0 dBm)\n* 2484 MHz [14] (20.0 dBm)\n"
	got := parseAPChannels(world)
	if !reflect.DeepEqual(got["2.4"], []int{1}) || len(got["5"]) != 0 || got == nil {
		t.Fatalf("world: %v", got)
	}
	// No frequency at all: unknown, not "none allowed".
	if got := parseAPChannels("command failed: No such device (-19)"); got != nil {
		t.Fatalf("no frequencies: %v", got)
	}
}

func TestAllowedWifiAPChannel(t *testing.T) {
	allowed := parseAPChannels(phyInfoDE)
	ap := proto.WifiAP{Enabled: true, Band: "5", Channel: 36}
	if err := allowedWifiAPChannel(ap, allowed); err != nil {
		t.Fatal(err)
	}
	ap.Channel = 52
	if allowedWifiAPChannel(ap, allowed) == nil {
		t.Fatal("radar channel accepted")
	}
	ap.Channel = 0
	if err := allowedWifiAPChannel(ap, allowed); err != nil {
		t.Fatal(err)
	}
	// Turning it off, or with the channels unknown (Wi-Fi off), always.
	ap.Channel = 52
	if err := allowedWifiAPChannel(ap, nil); err != nil {
		t.Fatal(err)
	}
	ap.Enabled = false
	if err := allowedWifiAPChannel(ap, allowed); err != nil {
		t.Fatal(err)
	}
	// No 5 GHz channel allowed (no country): saved all the same, S30wifi
	// runs it on 2.4 GHz meanwhile. Not so for 2.4 GHz.
	ap = proto.WifiAP{Enabled: true, Band: "5", Channel: 36}
	if err := allowedWifiAPChannel(ap, map[string][]int{"2.4": {1}}); err != nil {
		t.Fatal(err)
	}
	ap = proto.WifiAP{Enabled: true, Band: "2.4"}
	if allowedWifiAPChannel(ap, map[string][]int{"5": {36}}) == nil {
		t.Fatal("2.4 GHz without an allowed channel accepted")
	}
}

// The access point status tells when a 5 GHz one runs on 2.4 GHz, and why.
func TestWifiAPBandFallback(t *testing.T) {
	de := parseAPChannels(phyInfoDE)
	world := map[string][]int{"2.4": {1, 6, 11}}
	cases := []struct {
		band, running, country string
		allowed                map[string][]int
		inUse, fallback        string
	}{
		// Running: the band S30wifi used.
		{"5", "2.4", "00", world, "2.4", "country_unset"},
		{"5", "2.4", "XX", world, "2.4", "country_disallows"},
		{"5", "5", "DE", de, "5", ""},
		{"2.4", "2.4", "00", world, "2.4", ""},
		// Not running (off, or Wi-Fi off): where it would start.
		{"5", "", "00", nil, "2.4", "country_unset"},
		{"5", "", "00", world, "2.4", "country_unset"},
		{"5", "", "XX", world, "2.4", "country_disallows"},
		{"5", "", "DE", de, "5", ""},
		{"5", "", "DE", nil, "5", ""},
		{"2.4", "", "00", nil, "2.4", ""},
		// A run file that is not a band is ignored.
		{"5", "bogus", "DE", de, "5", ""},
	}
	for _, c := range cases {
		inUse, fallback := wifiAPBandFallback(c.band, c.running, c.country, c.allowed)
		if inUse != c.inUse || fallback != c.fallback {
			t.Errorf("%+v: got %q %q", c, inUse, fallback)
		}
	}
}

// The fields the web page reads.
func TestWifiAPRspFallbackJSON(t *testing.T) {
	data, err := json.Marshal(proto.WifiAPRsp{WifiAP: proto.WifiAP{Band: "5"}, BandInUse: "2.4", BandFallback: wifiAPFallbackCountryUnset})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"band":"5"`, `"bandInUse":"2.4"`, `"bandFallback":"country_unset"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s lacks %s", data, want)
		}
	}
	data, _ = json.Marshal(proto.WifiAPRsp{WifiAP: proto.WifiAP{Band: "5"}, BandInUse: "5"})
	if strings.Contains(string(data), "bandFallback") {
		t.Errorf("fallback without one: %s", data)
	}
}
