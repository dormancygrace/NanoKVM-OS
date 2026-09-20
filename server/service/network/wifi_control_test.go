package network

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestWifiFrequencies(t *testing.T) {
	out := parseFrequencies("* 2412.0 MHz [1] (20.0 dBm)\n* 5180 MHz [36] (20.0 dBm)\n* 5260 MHz [52] (disabled)\n* 5975 MHz [5] (20.0 dBm)")
	want := map[string][]string{"2.4": {"2412"}, "5": {"5180"}}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("%v", out)
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

func TestWifiHardwareAndStatus(t *testing.T) {
	root := t.TempDir()
	net := filepath.Join(root, "wlan0")
	etc := filepath.Join(root, "etc")
	if err := os.MkdirAll(etc, 0700); err != nil {
		t.Fatal(err)
	}
	w := radioControl{etc: etc, net: net, run: func(name string, args ...string) (string, error) {
		if name == "iw" {
			return "* 2412 MHz [1]\n* 5180 MHz [36]", nil
		}
		return "wpa_state=COMPLETED\nssid=Actual\x20Network\nfreq=5180", nil
	}}
	if !w.enabled() || w.present() {
		t.Fatal("default-on / absent adapter")
	}
	if err := os.MkdirAll(filepath.Join(net, "device"), 0700); err != nil {
		t.Fatal(err)
	}
	phy := filepath.Join(root, "phy0")
	_ = os.Mkdir(phy, 0700)
	_ = os.Symlink(phy, filepath.Join(net, "phy80211"))
	_ = os.WriteFile(filepath.Join(net, "device/modalias"), []byte("sdio:c00v5449d0145"), 0600)
	_ = os.WriteFile(filepath.Join(etc, "wifi.ssid"), []byte("Stale configured network"), 0600)
	d := w.status()
	if !d.Supported || !d.Connected || d.Ssid != "Actual Network" || d.Model != "AIC8801" || d.Band != "5" || d.PreferredBand != "5" || len(d.Bands) != 2 {
		t.Fatalf("%+v", d)
	}
	_ = os.WriteFile(filepath.Join(etc, "wifi.disabled"), nil, 0600)
	d = w.status()
	if d.Enabled || d.Connected {
		t.Fatal("disabled radio shown connected")
	}
	_ = os.Remove(filepath.Join(etc, "wifi.disabled"))
	w.run = func(string, ...string) (string, error) { return "", errors.New("unavailable") }
	d = w.status()
	if d.Connected || len(d.Bands) != 0 {
		t.Fatal("invented state on command failure")
	}
}

func TestWifiProfileStorage(t *testing.T) {
	w := radioControl{etc: t.TempDir()}
	p := wifiProfile{Ssid: "Private", Password: "valid123", Hidden: true, Security: "personal", Band: "5"}
	if err := w.saveProfile(p, map[string][]string{"2.4": {"2412"}, "5": {"5180", "5200"}}, "5"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wifi.ssid", "wifi.pass", "wifi.hidden", "wifi.security", "wifi.freq_list", "wifi.freq_list_2.4", "wifi.freq_list_5", wifiBandPreferenceFile} {
		info, err := os.Stat(filepath.Join(w.etc, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	p.Security = "open"
	p.Password = ""
	if err := w.saveProfile(p, map[string][]string{"2.4": {"2412"}}, "2.4"); err != nil {
		t.Fatal(err)
	}
	password, _ := os.ReadFile(filepath.Join(w.etc, "wifi.pass"))
	if len(password) != 0 {
		t.Fatal("old secret retained for open profile")
	}
}

func TestWifiControlAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := wifiControl
	defer func() { wifiControl = previous; wifiMu.Lock(); wifiBusy = false; wifiError = ""; wifiMu.Unlock() }()
	root := t.TempDir()
	net := filepath.Join(root, "wlan0")
	restarts := 0
	_ = os.Mkdir(net, 0700)
	phy := filepath.Join(root, "phy0")
	_ = os.Mkdir(phy, 0700)
	_ = os.Symlink(phy, filepath.Join(net, "phy80211"))
	wifiControl = radioControl{etc: root, net: net, run: func(name string, args ...string) (string, error) {
		if name == "iw" {
			return "* 2412 MHz [1]\n* 5180 MHz [36]", nil
		}
		if name == WiFiScript {
			restarts++
		}
		return "", nil
	}}
	router := gin.New()
	s := NewService()
	router.POST("/enabled", s.SetWifiEnabled)
	router.POST("/band-preference", s.SetWifiBandPreference)
	router.POST("/configure", s.ConfigureWifi)
	router.GET("/scan", s.ScanWifi)
	request := func(method, path, body string) int {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(rec, req)
		var rsp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil {
			t.Fatal(err)
		}
		return rsp.Code
	}
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			wifiMu.Lock()
			busy := wifiBusy
			wifiMu.Unlock()
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("operation did not finish")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if request("POST", "/enabled", `{}`) == 0 {
		t.Fatal("missing bool accepted")
	}
	_ = writePrivateFile(filepath.Join(root, "wifi.ssid"), []byte("Saved"))
	_ = writePrivateFile(filepath.Join(root, "wifi.pass"), []byte("secret123"))
	if request("POST", "/enabled", `{"enabled":false}`) != 0 {
		t.Fatal("disable rejected")
	}
	wait()
	if wifiControl.enabled() {
		t.Fatal("disable not persisted")
	}
	pass, _ := os.ReadFile(filepath.Join(root, "wifi.pass"))
	if string(pass) != "secret123" {
		t.Fatal("disable deleted credentials")
	}
	if request("GET", "/scan?band=5", "") == 0 {
		t.Fatal("scan accepted while off")
	}
	if request("POST", "/configure", `{"ssid":"Other","password":"secret123","band":"5","security":"personal"}`) == 0 {
		t.Fatal("connect accepted while off")
	}
	if request("POST", "/enabled", `{"enabled":true}`) != 0 {
		t.Fatal("enable rejected")
	}
	wait()
	if !wifiControl.enabled() {
		t.Fatal("enable not persisted")
	}
	beforePreference := restarts
	if request("POST", "/band-preference", `{"preferredBand":"2.4"}`) != 0 {
		t.Fatal("preference rejected")
	}
	if restarts != beforePreference {
		t.Fatal("preference change restarted Wi-Fi")
	}
	preference, _ := os.ReadFile(filepath.Join(root, wifiBandPreferenceFile))
	if string(preference) != "2.4" {
		t.Fatal("preference not persisted")
	}
	if request("POST", "/configure", `{"ssid":"Hidden","password":"secret123","band":"5","hidden":true,"security":"personal"}`) != 0 {
		t.Fatal("valid configure rejected")
	}
	wait()
	hidden, _ := os.ReadFile(filepath.Join(root, "wifi.hidden"))
	freq, _ := os.ReadFile(filepath.Join(root, "wifi.freq_list"))
	if string(hidden) != "true" || string(freq) != "2412 5180" {
		t.Fatal("profile options not persisted")
	}
	wifiControl.run = func(string, ...string) (string, error) { return "", errors.New("command failed") }
	if request("POST", "/enabled", `{"enabled":false}`) != 0 {
		t.Fatal("disable not scheduled")
	}
	wait()
	if !wifiControl.enabled() {
		t.Fatal("failed command changed persisted enable state")
	}
	wifiMu.Lock()
	operationError := wifiError
	wifiMu.Unlock()
	if operationError == "" {
		t.Fatal("async error lost")
	}
	wifiControl.net = filepath.Join(root, "absent")
	if request("POST", "/enabled", `{"enabled":true}`) == 0 {
		t.Fatal("absent adapter enabled")
	}
}

func TestWifiAdvertisedSecurity(t *testing.T) {
	for _, tc := range []struct{ ies, want string }{
		{"WPA:\n * Authentication suites: PSK", "wpa"},
		{"RSN:\n * Authentication suites: PSK", "wpa2"},
		{"RSN:\n * Authentication suites: SAE", "wpa3"},
		{"RSN:\n * Authentication suites: PSK SAE", "wpa2-wpa3"},
		{"WPA:\n * Authentication suites: PSK\nRSN:\n * Authentication suites: PSK", "wpa-wpa2"},
	} {
		got := parseScan("BSS 00:11:22:33:44:55(on wlan0)\n freq: 5180.0\n SSID: Example\n capability: ESS Privacy\n"+tc.ies, "5")
		if len(got) != 1 || got[0].Security != tc.want {
			t.Fatalf("want %s, got %+v", tc.want, got)
		}
	}
}

func TestWifiScanAllBands(t *testing.T) {
	out := `BSS 00:11:22:33:44:01(on wlan0)
 freq: 2412
 signal: -40.00 dBm
 SSID: Shared
BSS 00:11:22:33:44:02(on wlan0)
 freq: 5180
 signal: -50.00 dBm
 SSID: Shared
BSS 00:11:22:33:44:03(on wlan0)
 freq: 5200
 signal: -60.00 dBm
 SSID: Shared
BSS 00:11:22:33:44:04(on wlan0)
 freq: 6105
 SSID: Unsupported band
`
	got := parseScan(out, "all")
	if len(got) != 2 || got[0].Band != "2.4" || got[1].Band != "5" || got[1].Signal != -50 {
		t.Fatalf("all-band scan lost a band or kept duplicate/unsupported entries: %+v", got)
	}
}
