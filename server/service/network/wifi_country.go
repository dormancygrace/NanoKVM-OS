package network

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The regulatory country of Wi-Fi: an ISO 3166-1 alpha-2 code in capitals,
// or 00, the world domain, when none is set. S30wifi sets it (iw reg set)
// before hostapd or wpa_supplicant starts and names it in their
// configuration. The AIC8800 driver leaves regulation to cfg80211, which
// applies it from the kernel's regulatory database.
const wifiCountryFile = "wifi.country"

// validWifiCountry: two capital letters, or 00. The code reaches iw's argv and
// the generated configuration files, so nothing else passes.
func validWifiCountry(code string) bool {
	if code == "00" {
		return true
	}
	return len(code) == 2 && code[0] >= 'A' && code[0] <= 'Z' && code[1] >= 'A' && code[1] <= 'Z'
}

// country is the saved country; 00 when none is saved or the file is invalid,
// as S30wifi reads it.
func (w radioControl) country() string {
	data, err := os.ReadFile(filepath.Join(w.etc, wifiCountryFile))
	if err != nil {
		return "00"
	}
	line, _, _ := strings.Cut(string(data), "\n")
	if code := strings.TrimSuffix(line, "\r"); validWifiCountry(code) {
		return code
	}
	return "00"
}

func (w radioControl) saveCountry(code string) error {
	if !validWifiCountry(code) {
		return errors.New("invalid country")
	}
	return writeFileAtomic(filepath.Join(w.etc, wifiCountryFile), []byte(code+"\n"))
}

// parseRegCountry reads the country of the global regulatory domain from
// "iw reg get": the first "country XX:" line before any "phy#N" section
// (a self-managed wiphy's own domain). iw before 4.x prints no "global"
// header and no sections. cfg80211 also reports 98 (an intersection) and 99
// (built by a driver); "" when there is none.
func parseRegCountry(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "phy#") {
			break
		}
		rest, ok := strings.CutPrefix(line, "country ")
		if !ok {
			continue
		}
		code, _, ok := strings.Cut(rest, ":")
		if ok && len(code) == 2 && regCode.MatchString(code) {
			return code
		}
	}
	return ""
}

var regCode = regexp.MustCompile(`^[0-9A-Z]{2}$`)

// activeCountry is the country in force; "" when iw cannot tell (cfg80211
// is not loaded while Wi-Fi is off).
func (w radioControl) activeCountry() string {
	out, err := w.run("iw", "reg", "get")
	if err != nil {
		return ""
	}
	return parseRegCountry(out)
}

var apFrequencyLine = regexp.MustCompile(`\*\s+(\d+)(?:\.\d+)?\s+MHz`)

// parseAPChannels lists, per band, the channels "iw phy <phy> info" offers an
// access point: not disabled, without "no IR" (initiating radiation: beacons;
// "passive scanning"/"no IBSS" in older iw) and without radar detection,
// which hostapd would need DFS for. The 4.9 GHz channels of 5 GHz are left
// out, as S30wifi does. nil when the output lists no frequency at all: the
// channels are unknown, unlike a band whose every channel is excluded.
func parseAPChannels(output string) map[string][]int {
	var result map[string][]int
	for _, line := range strings.Split(output, "\n") {
		m := apFrequencyLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if result == nil {
			result = map[string][]int{}
		}
		freq, _ := strconv.Atoi(m[1])
		band, channel := bandFor(freq), channelFor(freq)
		// Channel 14 is 802.11b only, which hostapd's hw_mode=g cannot use.
		if band == "" || channel == 0 || channel == 14 || (band == "5" && freq < 5150) {
			continue
		}
		excluded := false
		for _, flag := range []string{"disabled", "no IR", "radar", "passive scanning", "no IBSS"} {
			excluded = excluded || strings.Contains(line, flag)
		}
		if !excluded {
			result[band] = append(result[band], channel)
		}
	}
	return result
}

// apChannelCache keeps the access point channels of a phy for a while: the
// access point page polls every few seconds. A country change clears it.
type apChannelCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	phy      string
	at       time.Time
	channels map[string][]int
}

func (c *apChannelCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.channels = nil
}

func (c *bandCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.freqs = nil
}

// apChannels: the channels an access point may use now, per band; nil while
// unknown (Wi-Fi off, the driver not loaded, or iw failed).
func (w radioControl) apChannels() map[string][]int {
	phy := w.phy()
	if phy == "" {
		return nil
	}
	read := func() map[string][]int {
		out, err := w.run("iw", "phy", phy, "info")
		if err != nil {
			return nil
		}
		return parseAPChannels(out)
	}
	c := w.apBands
	if c == nil {
		return read()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.channels != nil && c.phy == phy && now.Sub(c.at) >= 0 && now.Sub(c.at) < c.ttl {
		return c.channels
	}
	c.channels = read()
	c.phy, c.at = phy, now
	return c.channels
}

// countryChanged: the channel lists follow the new domain at once.
func (w radioControl) countryChanged() {
	if w.bands != nil {
		w.bands.reset()
	}
	if w.apBands != nil {
		w.apBands.reset()
	}
}

func (s *Service) GetWifiCountry(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, &proto.WifiCountryRsp{Country: wifiControl.country(), Active: wifiControl.activeCountry()})
}

// SetWifiCountry saves the country and applies it: iw sets the domain, then
// S30wifi restarts Wi-Fi so that hostapd or wpa_supplicant start with it, as
// at boot. The reply goes out first, as both can cut off a page that reaches
// the device over Wi-Fi. With Wi-Fi off, the next start applies it.
func (s *Service) SetWifiCountry(c *gin.Context) {
	var req proto.SetWifiCountryReq
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || !validWifiCountry(req.Country) {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	if !checkRadio(c, false) {
		return
	}
	previous := wifiControl.country()
	if err := wifiControl.saveCountry(req.Country); err != nil {
		finishWifi(err)
		log.Errorf("failed to save the Wi-Fi country: %s", err)
		rsp.ErrRsp(c, -1, "failed to save the Wi-Fi country")
		return
	}
	log.Infof("set the Wi-Fi country: %s (was %s)", req.Country, previous)
	if !wifiControl.present() || !wifiControl.enabled() {
		finishWifi(nil)
		rsp.OkRspWithData(c, &proto.WifiCountryRsp{Country: req.Country, Active: wifiControl.activeCountry()})
		return
	}
	active := wifiControl.activeCountry()
	if req.Country == previous && active == req.Country {
		finishWifi(nil)
		rsp.OkRspWithData(c, &proto.WifiCountryRsp{Country: req.Country, Active: active})
		return
	}
	// Acknowledge before the radio changes: a channel the new country does
	// not allow drops the association carrying this request at once. Active
	// is still the previous domain; GET reads the new one.
	rsp.OkRspWithData(c, &proto.WifiCountryRsp{Country: req.Country, Active: active})
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		// S30wifi sets the domain again at start and waits for it; setting it
		// first lets the channel lists follow even if the restart fails.
		if _, err := wifiControl.run("iw", "reg", "set", req.Country); err != nil {
			log.Warnf("iw reg set %s failed: %v", req.Country, err)
		}
		_, err := wifiControl.run(WiFiScript, "restart")
		wifiControl.countryChanged()
		finishWifi(err)
	}()
}
