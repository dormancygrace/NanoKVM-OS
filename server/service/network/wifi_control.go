package network

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

// One lock covers radio operations, including the legacy provisioning API.
var wifiMu sync.Mutex
var wifiBusy bool
var wifiError string
var wifiControl = radioControl{etc: "/etc/kvm", net: "/sys/class/net/wlan0", run: runRadioCommand}

type radioControl struct {
	etc, net string
	run      func(string, ...string) (string, error)
}

func runRadioCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	return string(out), err
}

func (w radioControl) present() bool { _, err := os.Stat(w.net); return err == nil }
func (w radioControl) enabled() bool {
	_, err := os.Stat(filepath.Join(w.etc, "wifi.disabled"))
	return os.IsNotExist(err)
}

func (w radioControl) model() string {
	alias, _ := os.ReadFile(filepath.Join(w.net, "device/modalias"))
	id := strings.ToUpper(string(alias))
	for _, pair := range [][2]string{{"V5449D0145", "AIC8801"}, {"V544AD0146", "AIC8801"}, {"VC8A1D0082", "AIC8800D80"}, {"VC8A1DC08D", "AIC8800D80"}, {"V024CDB73A", "RTL8733BS"}, {"V024CDB733", "RTL8733BS"}} {
		if strings.Contains(id, pair[0]) {
			return pair[1]
		}
	}
	driver, err := filepath.EvalSymlinks(filepath.Join(w.net, "device/driver"))
	if err == nil {
		return filepath.Base(driver)
	}
	return ""
}

var frequencyLine = regexp.MustCompile(`\*\s+(\d+)(?:\.0+)? MHz`)

func bandFor(freq int) string {
	if freq >= 2400 && freq < 2500 {
		return "2.4"
	}
	if freq >= 4900 && freq < 5925 {
		return "5"
	}
	return ""
}
func parseFrequencies(output string) map[string][]string {
	result := map[string][]string{}
	for _, line := range strings.Split(output, "\n") {
		m := frequencyLine.FindStringSubmatch(line)
		if m == nil || strings.Contains(line, "disabled") {
			continue
		}
		freq, _ := strconv.Atoi(m[1])
		band := bandFor(freq)
		if band != "" {
			result[band] = append(result[band], m[1])
		}
	}
	return result
}
func (w radioControl) frequencies() map[string][]string {
	phy, err := filepath.EvalSymlinks(filepath.Join(w.net, "phy80211"))
	if err != nil {
		return map[string][]string{}
	}
	out, err := w.run("iw", "phy", filepath.Base(phy), "info")
	if err != nil {
		return map[string][]string{}
	}
	return parseFrequencies(out)
}

type wifiNetwork struct {
	Bssid    string  `json:"bssid"`
	Ssid     string  `json:"ssid"`
	Band     string  `json:"band"`
	Signal   float64 `json:"signal"`
	Security string  `json:"security"`
}

// iw escapes non-printable SSID bytes as \xNN; preserve spaces and Unicode.
func decodeSSID(value string) string {
	var out []byte
	for i := 0; i < len(value); i++ {
		if i+3 < len(value) && value[i] == '\\' && value[i+1] == 'x' {
			if b, err := strconv.ParseUint(value[i+2:i+4], 16, 8); err == nil {
				out = append(out, byte(b))
				i += 3
				continue
			}
		}
		out = append(out, value[i])
	}
	return string(out)
}
func parseScan(output, band string) []wifiNetwork {
	result := []wifiNetwork{}
	var n *wifiNetwork
	var privacy, secured, wpa, psk, sae, unsupported bool
	var section string
	flush := func() {
		if n == nil || n.Band == "" || (band != "all" && n.Band != band) || n.Ssid == "" || !utf8.ValidString(n.Ssid) {
			return
		}
		n.Security = "open"
		if privacy || secured {
			n.Security = "unsupported"
		}
		if !unsupported {
			switch {
			case psk && sae:
				n.Security = "wpa2-wpa3"
			case sae:
				n.Security = "wpa3"
			case psk && wpa:
				n.Security = "wpa-wpa2"
			case psk:
				n.Security = "wpa2"
			case wpa:
				n.Security = "wpa"
			}
		}
		result = append(result, *n)
	}
	for _, line := range strings.Split(output, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "BSS ") {
			flush()
			fields := strings.Fields(l)
			if len(fields) < 2 {
				n = nil
				continue
			}
			mac := strings.Split(fields[1], "(")[0]
			n = &wifiNetwork{Bssid: mac, Signal: -100}
			privacy = false
			secured = false
			wpa = false
			psk = false
			sae = false
			section = ""
			unsupported = false
		} else if n != nil {
			switch {
			case strings.HasPrefix(l, "freq:"):
				freq, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(l, "freq:")), 64)
				n.Band = bandFor(int(freq))
			case strings.HasPrefix(l, "signal:"):
				fields := strings.Fields(strings.TrimPrefix(l, "signal:"))
				if len(fields) > 0 {
					n.Signal, _ = strconv.ParseFloat(fields[0], 64)
				}
			case strings.HasPrefix(strings.TrimLeft(line, "\t "), "SSID: "):
				n.Ssid = decodeSSID(strings.TrimPrefix(strings.TrimLeft(line, "\t "), "SSID: "))
			case strings.HasPrefix(l, "capability:"):
				privacy = strings.Contains(l, "Privacy")
			case strings.HasPrefix(l, "RSN:"):
				secured = true
				section = "rsn"
			case strings.HasPrefix(l, "WPA:"):
				secured = true
				section = "wpa"
			case strings.Contains(l, "Authentication suites:"):
				if section == "rsn" {
					psk = psk || strings.Contains(l, "PSK")
					sae = sae || strings.Contains(l, "SAE")
				}
				if section == "wpa" {
					wpa = wpa || strings.Contains(l, "PSK")
				}
				unsupported = unsupported || strings.Contains(l, "IEEE 802.1X") || strings.Contains(l, "OWE")
			}
		}
	}
	flush()
	sort.SliceStable(result, func(i, j int) bool { return result[i].Signal > result[j].Signal })
	// Multiple APs advertising the same network need only one row, strongest first.
	seen := map[string]bool{}
	unique := []wifiNetwork{}
	for _, n := range result {
		key := n.Ssid + "\x00" + n.Security + "\x00" + n.Band
		if !seen[key] {
			seen[key] = true
			unique = append(unique, n)
		}
	}
	return unique
}

func (w radioControl) status() *proto.GetWifiRsp {
	d := &proto.GetWifiRsp{Supported: w.present(), Enabled: w.enabled(), Bands: []string{}, ApMode: isAPMode()}
	wifiMu.Lock()
	d.Busy = wifiBusy
	d.Error = wifiError
	wifiMu.Unlock()
	if !d.Supported {
		return d
	}
	d.Model = w.model()
	freqs := w.frequencies()
	for _, b := range []string{"2.4", "5"} {
		if len(freqs[b]) > 0 {
			d.Bands = append(d.Bands, b)
		}
	}
	if !d.Enabled || d.ApMode {
		return d
	}
	out, err := w.run("wpa_cli", "-i", "wlan0", "status")
	if err != nil {
		return d
	}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "wpa_state":
			d.Connected = value == "COMPLETED"
		case "ssid":
			d.Ssid = decodeSSID(value)
		case "freq":
			freq, _ := strconv.Atoi(value)
			d.Band = bandFor(freq)
		}
	}
	if !d.Connected {
		d.Ssid = ""
		d.Band = ""
	}
	return d
}

type wifiProfile struct {
	Ssid     string `json:"ssid"`
	Password string `json:"password"`
	Band     string `json:"band"`
	Hidden   bool   `json:"hidden"`
	Security string `json:"security"`
}

func (p wifiProfile) validate() error {
	validText := func(s string) bool { return utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n") }
	if len(p.Ssid) < 1 || len(p.Ssid) > 32 || !validText(p.Ssid) {
		return errors.New("invalid SSID")
	}
	if p.Band != "2.4" && p.Band != "5" {
		return errors.New("invalid band")
	}
	switch p.Security {
	case "open":
		if p.Password != "" {
			return errors.New("open network must not have a password")
		}
	case "personal", "wpa", "wpa-wpa2", "wpa2", "wpa3", "wpa2-wpa3":
		if len(p.Password) < 8 || len(p.Password) > 63 || !validText(p.Password) {
			return errors.New("invalid password")
		}
	default:
		return errors.New("unsupported security")
	}
	return nil
}

// Restore the previous profile if any persistent write fails.
func (w radioControl) saveProfile(p wifiProfile, frequencies []string) error {
	values := map[string]string{"wifi.ssid": p.Ssid, "wifi.pass": p.Password, "wifi.security": p.Security, "wifi.hidden": strconv.FormatBool(p.Hidden), "wifi.freq_list": strings.Join(frequencies, " ")}
	type previous struct {
		data   []byte
		exists bool
	}
	before := map[string]previous{}
	for name := range values {
		data, err := os.ReadFile(filepath.Join(w.etc, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		before[name] = previous{data, err == nil}
	}
	for name, value := range values {
		if err := writePrivateFile(filepath.Join(w.etc, name), []byte(value)); err != nil {
			for name, old := range before {
				path := filepath.Join(w.etc, name)
				if old.exists {
					_ = writePrivateFile(path, old.data)
				} else {
					_ = os.Remove(path)
				}
			}
			return err
		}
	}
	return nil
}

func reserveWifi() bool {
	wifiMu.Lock()
	defer wifiMu.Unlock()
	if wifiBusy {
		return false
	}
	wifiBusy = true
	wifiError = ""
	return true
}
func finishWifi(err error) {
	wifiMu.Lock()
	defer wifiMu.Unlock()
	wifiBusy = false
	if err != nil {
		wifiError = "operationFailed"
	}
}
func checkRadio(c *gin.Context, enabled bool) bool {
	var rsp proto.Response
	if !wifiControl.present() || isAPMode() || (enabled && !wifiControl.enabled()) {
		rsp.ErrRsp(c, -1, "Wi-Fi unavailable")
		return false
	}
	if !reserveWifi() {
		rsp.ErrRsp(c, -2, "Wi-Fi busy")
		return false
	}
	return true
}

func (s *Service) ScanWifi(c *gin.Context) {
	var rsp proto.Response
	band := c.Query("band")
	if band != "2.4" && band != "5" && band != "all" {
		rsp.ErrRsp(c, -1, "invalid band")
		return
	}
	if !checkRadio(c, true) {
		return
	}
	defer finishWifi(nil)
	available := wifiControl.frequencies()
	freqs := available[band]
	if band == "all" {
		freqs = append(append([]string{}, available["2.4"]...), available["5"]...)
	}
	if len(freqs) == 0 {
		rsp.ErrRsp(c, -1, "band unavailable")
		return
	}
	args := append([]string{"dev", "wlan0", "scan", "freq"}, freqs...)
	out, err := wifiControl.run("iw", args...)
	if err != nil {
		rsp.ErrRsp(c, -1, "scan failed")
		return
	}
	rsp.OkRspWithData(c, parseScan(out, band))
}

func (s *Service) SetWifiEnabled(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || req.Enabled == nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	if !checkRadio(c, false) {
		return
	}
	enabled := *req.Enabled
	previousEnabled := wifiControl.enabled()
	// Acknowledge before changing the interface carrying this HTTP request.
	rsp.OkRsp(c)
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		path := filepath.Join(wifiControl.etc, "wifi.disabled")
		var err error
		if enabled {
			err = os.Remove(path)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = writePrivateFile(path, nil)
		}
		if err == nil {
			action := "disable"
			if enabled {
				action = "restart"
			}
			_, err = wifiControl.run(WiFiScript, action)
		}
		if err != nil {
			if previousEnabled {
				_ = os.Remove(path)
			} else {
				_ = writePrivateFile(path, nil)
			}
		}
		finishWifi(err)
	}()
}

func (s *Service) ConfigureWifi(c *gin.Context) {
	var req wifiProfile
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || req.validate() != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	if !checkRadio(c, true) {
		return
	}
	freqs := wifiControl.frequencies()[req.Band]
	if len(freqs) == 0 {
		finishWifi(nil)
		rsp.ErrRsp(c, -1, "band unavailable")
		return
	}
	if err := wifiControl.saveProfile(req, freqs); err != nil {
		finishWifi(err)
		rsp.ErrRsp(c, -1, "failed to save Wi-Fi")
		return
	}
	rsp.OkRsp(c)
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, err := wifiControl.run(WiFiScript, "restart")
		finishWifi(err)
	}()
}
