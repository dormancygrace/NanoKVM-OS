package network

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/internal/networktrial"
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

func (w radioControl) countryPath() string { return filepath.Join(w.etc, wifiCountryFile) }

// country is the saved country; 00 when none is saved or the file is invalid,
// as S30wifi reads it.
func (w radioControl) country() string {
	data, err := os.ReadFile(w.countryPath())
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
	return writeFileAtomic(w.countryPath(), []byte(code+"\n"))
}

// The kernel's regulatory database, from wireless-regdb (platform/build.sh).
// cfg80211 applies only a country it lists: "iw reg set" takes any other code
// without an error and leaves the previous domain in force.
var regulatoryDB = "/lib/firmware/regulatory.db"

// parseRegulatoryDB lists the countries of a regulatory.db as cfg80211 reads
// it: a header (magic "RGDB", version 20), then one 4-byte entry per country,
// its code and a 16-bit pointer to its rules, up to an entry whose pointer
// is 0.
func parseRegulatoryDB(data []byte) ([]string, error) {
	if len(data) < 8 || string(data[:4]) != "RGDB" {
		return nil, errors.New("not a regulatory database")
	}
	if version := binary.BigEndian.Uint32(data[4:8]); version != 20 {
		return nil, fmt.Errorf("regulatory database version %d", version)
	}
	var codes []string
	for offset := 8; ; offset += 4 {
		if offset+4 > len(data) {
			return nil, errors.New("regulatory database without the end of its country list")
		}
		if binary.BigEndian.Uint16(data[offset+2:offset+4]) == 0 {
			return codes, nil
		}
		if code := string(data[offset : offset+2]); validWifiCountry(code) {
			codes = append(codes, code)
		}
	}
}

// supportedCountries: the countries cfg80211 can apply, 00 among them, from
// the database it reads. nil when the database cannot be read, which does
// not happen on the device: then every valid code is taken, and S30wifi
// still falls back to the world domain for one that does not come into force.
func supportedCountries() []string {
	data, err := os.ReadFile(regulatoryDB)
	if err != nil {
		log.Warnf("cannot read the regulatory database: %s", err)
		return nil
	}
	codes, err := parseRegulatoryDB(data)
	if err != nil {
		log.Warnf("cannot read the regulatory database %s: %s", regulatoryDB, err)
		return nil
	}
	return codes
}

func countrySupported(code string, supported []string) bool {
	return code == "00" || supported == nil || slices.Contains(supported, code)
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
	rsp.OkRspWithData(c, &proto.WifiCountryRsp{Country: wifiControl.country(), Active: wifiControl.activeCountry(),
		Supported: supportedCountries()})
}

// applyWifiCountry puts a country in force: iw sets the domain, then S30wifi
// restarts Wi-Fi so that hostapd or wpa_supplicant start with it, as at boot.
// S30wifi sets the domain again at start and waits for it; setting it first
// lets the channel lists follow even if the restart fails.
func applyWifiCountry(code string) error {
	if _, err := wifiControl.run("iw", "reg", "set", code); err != nil {
		log.Warnf("iw reg set %s failed: %v", code, err)
	}
	_, err := wifiControl.run(WiFiScript, "restart")
	wifiControl.countryChanged()
	return err
}

// restartWifiCountry applies the saved country after a trial restored it.
// With Wi-Fi off the next start applies it.
func restartWifiCountry() error {
	if !wifiControl.enabled() {
		wifiControl.countryChanged()
		return nil
	}
	return applyWifiCountry(wifiControl.country())
}

// requestMayRideWifi, which tests replace: a test request has no local
// address, and so always rides Wi-Fi.
var countryRequestMayRideWifi = requestMayRideWifi

// The reply to a change: with a trial when it runs on one.
type wifiCountryReply struct {
	proto.WifiCountryRsp
	Trial *trialInfo `json:"trial,omitempty"`
}

// SetWifiCountry saves the country and applies it (applyWifiCountry). The
// reply goes out first, as the restart can cut off a page that reaches the
// device over Wi-Fi. With Wi-Fi off, the next start applies it.
//
// It is a network change like the access point's (trial.go): none while
// another is on trial or being undone, whose revert would bring back its own
// settings under this domain. Over Wi-Fi, or a VPN that may run over it, it
// runs on trial itself: a domain that does not allow the channel of the
// network the device joined, or of its access point, cuts this page off, and
// the trial's revert restores the previous country and restarts Wi-Fi with
// it. Over Ethernet or USB the page stays, with nothing to confirm.
func (s *Service) SetWifiCountry(c *gin.Context) {
	var req proto.SetWifiCountryReq
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || !validWifiCountry(req.Country) {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	// A country the database lacks would never come into force.
	if !countrySupported(req.Country, supportedCountries()) {
		rsp.ErrRsp(c, -1, "the regulatory database has no rules for the country "+req.Country)
		return
	}
	if !checkRadio(c, false) {
		return
	}
	previous := wifiControl.country()
	active := wifiControl.activeCountry()
	restart := wifiControl.present() && wifiControl.enabled() && (req.Country != previous || active != req.Country)
	reply := wifiCountryReply{WifiCountryRsp: proto.WifiCountryRsp{Country: req.Country, Active: active}}
	saveFailed := func(err error) {
		log.Errorf("failed to save the Wi-Fi country: %s", err)
		finishWifi(err)
		rsp.ErrRsp(c, -2, "failed to save the Wi-Fi country")
	}

	if !restart || !countryRequestMayRideWifi(c) {
		err := withoutTrial(func() error { return wifiControl.saveCountry(req.Country) })
		if errors.Is(err, errTrialPending) || errors.Is(err, errTrialUndoing) {
			finishWifi(nil)
			rsp.ErrRsp(c, -3, err.Error())
			return
		}
		if err != nil {
			saveFailed(err)
			return
		}
		_ = syncFiles()
		log.Infof("set the Wi-Fi country: %s (was %s)", req.Country, previous)
		if !restart {
			finishWifi(nil)
			if !wifiControl.present() || !wifiControl.enabled() {
				reply.Active = wifiControl.activeCountry()
			}
			rsp.OkRspWithData(c, &reply)
			return
		}
		// Active is still the previous domain; GET reads the new one.
		rsp.OkRspWithData(c, &reply)
		c.Writer.Flush()
		// Shutdown and standby wait for the restart (networktrial.CheckIdle).
		releaseRestart := networktrial.Begin()
		go func() {
			defer releaseRestart()
			time.Sleep(restartDelay)
			finishWifi(applyWifiCountry(req.Country))
		}()
		return
	}

	trial, err := beginTrial("wifi-country", []string{wifiControl.countryPath()})
	if err != nil {
		finishWifi(nil)
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	if err := wifiControl.saveCountry(req.Country); err != nil {
		trial.abandon()
		saveFailed(err)
		return
	}
	_ = syncFiles()
	trial.start()
	info := trial.info(trialHosts("", c.Request.Host))
	reply.Trial = &info
	// Acknowledge before the radio changes: a channel the new country does
	// not allow drops the association carrying this request at once.
	rsp.OkRspWithData(c, &reply)
	c.Writer.Flush()
	releaseRestart := networktrial.Begin()
	go func() {
		defer releaseRestart()
		time.Sleep(restartDelay)
		err := applyWifiCountry(req.Country)
		trial.restarted(err)
		finishWifi(err)
	}()
	log.Infof("set the Wi-Fi country on trial: %s (was %s)", req.Country, previous)
}
