package network

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Saved Wi-Fi networks: every network the device has joined, one directory
// per network under /etc/kvm/wifi.saved named by the hex of its SSID, with the
// files S30wifi reads (ssid, pass, security, hidden, auto). S30wifi joins any
// saved network whose auto file is 1, and the network chosen in this boot
// (/run/nanokvm-wifi/selected) even without it. The top-level wifi.ssid,
// wifi.pass, ... files stay a copy of the last chosen network for recovery,
// the installers and older scripts.
const (
	wifiSavedDir     = "wifi.saved"
	wifiSelectedFile = "selected"
	wifiRunDir       = "/run/nanokvm-wifi"
	// Bounds the configuration S30wifi writes and the list on the page; the
	// network joined longest ago goes first.
	maxSavedNetworks = 32
)

type savedNetwork struct {
	Ssid        string `json:"ssid"`
	Security    string `json:"security"`
	Hidden      bool   `json:"hidden"`
	AutoConnect bool   `json:"autoConnect"`
	Current     bool   `json:"current"`
	used        time.Time
}

func savedName(ssid string) string { return hex.EncodeToString([]byte(ssid)) }

func (w radioControl) savedPath(ssid string) string {
	return filepath.Join(w.etc, wifiSavedDir, savedName(ssid))
}

// selectedPath is empty for a radioControl without a run directory (tests
// that do not look at the selection); nothing is then recorded.
func (w radioControl) selectedPath() string {
	if w.runDir == "" {
		return ""
	}
	return filepath.Join(w.runDir, wifiSelectedFile)
}

// saveNetwork stores p, replacing a saved network with the same SSID. The
// files are written to a hidden directory first, which S30wifi skips, and
// then renamed into place.
func (w radioControl) saveNetwork(p wifiProfile, auto bool) error {
	dir := filepath.Join(w.etc, wifiSavedDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	name := savedName(p.Ssid)
	tmp := filepath.Join(dir, "."+name+".tmp")
	old := filepath.Join(dir, "."+name+".old")
	final := filepath.Join(dir, name)
	_ = os.RemoveAll(tmp)
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return err
	}
	security := p.Security
	if security == "" {
		security = "personal"
	}
	autoValue := "0"
	if auto {
		autoValue = "1"
	}
	hidden := "false"
	if p.Hidden {
		hidden = "true"
	}
	for file, value := range map[string]string{"ssid": p.Ssid, "pass": p.Password,
		"security": security, "hidden": hidden, "auto": autoValue} {
		if err := writePrivateFile(filepath.Join(tmp, file), []byte(value)); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	_ = os.RemoveAll(old)
	if err := os.Rename(final, old); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Rename(old, final)
		_ = os.RemoveAll(tmp)
		return err
	}
	_ = os.RemoveAll(old)
	return w.trimSaved(name)
}

// trimSaved removes the networks joined longest ago beyond maxSavedNetworks,
// never keep (the one just saved).
func (w radioControl) trimSaved(keep string) error {
	networks, err := w.readSaved()
	if err != nil || len(networks) <= maxSavedNetworks {
		return err
	}
	sort.Slice(networks, func(i, j int) bool { return networks[i].used.Before(networks[j].used) })
	for _, n := range networks[:len(networks)-maxSavedNetworks] {
		if savedName(n.Ssid) == keep {
			continue
		}
		if err := os.RemoveAll(w.savedPath(n.Ssid)); err != nil {
			return err
		}
	}
	return nil
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}

// readSaved lists the saved networks, without passwords. An entry whose name
// does not match its SSID is not one this code wrote and is left out, as
// S30wifi leaves it out.
func (w radioControl) readSaved() ([]savedNetwork, error) {
	dir := filepath.Join(w.etc, wifiSavedDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	current := readTrimmed(filepath.Join(w.etc, "wifi.ssid"))
	var networks []savedNetwork
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		ssid, err := os.ReadFile(filepath.Join(path, "ssid"))
		if err != nil || len(ssid) == 0 || savedName(string(ssid)) != entry.Name() {
			continue
		}
		n := savedNetwork{Ssid: string(ssid), Security: readTrimmed(filepath.Join(path, "security")),
			Hidden:      readTrimmed(filepath.Join(path, "hidden")) == "true",
			AutoConnect: readTrimmed(filepath.Join(path, "auto")) == "1",
			Current:     string(ssid) == current}
		if n.Security == "" {
			n.Security = "personal"
		}
		if info, err := entry.Info(); err == nil {
			n.used = info.ModTime()
		}
		networks = append(networks, n)
	}
	// The current network first, then the most recently joined.
	sort.SliceStable(networks, func(i, j int) bool {
		if networks[i].Current != networks[j].Current {
			return networks[i].Current
		}
		return networks[i].used.After(networks[j].used)
	})
	return networks, nil
}

// savedProfile returns a saved network with its password.
func (w radioControl) savedProfile(ssid string) (wifiProfile, bool, error) {
	path := w.savedPath(ssid)
	stored, err := os.ReadFile(filepath.Join(path, "ssid"))
	if err != nil {
		return wifiProfile{}, false, err
	}
	if string(stored) != ssid {
		return wifiProfile{}, false, os.ErrNotExist
	}
	pass, err := os.ReadFile(filepath.Join(path, "pass"))
	if err != nil && !os.IsNotExist(err) {
		return wifiProfile{}, false, err
	}
	p := wifiProfile{Ssid: ssid, Password: strings.TrimRight(string(pass), "\r\n"), Security: readTrimmed(filepath.Join(path, "security")),
		Hidden: readTrimmed(filepath.Join(path, "hidden")) == "true"}
	if p.Security == "" {
		p.Security = "personal"
	}
	return p, readTrimmed(filepath.Join(path, "auto")) == "1", nil
}

// migrateSaved stores the network of a release with one network as a saved
// one that connects automatically, as it did. S30wifi does the same at start;
// this covers an upgrade that has not restarted Wi-Fi yet.
func (w radioControl) migrateSaved() error {
	if _, err := os.Stat(filepath.Join(w.etc, wifiSavedDir)); !os.IsNotExist(err) {
		return err
	}
	if p, ok := w.configuredProfile(); ok {
		return w.saveNetwork(p, true)
	}
	return nil
}

// configuredProfile is the last chosen network from the top-level files, as
// S30wifi reads them: line breaks at the end are not part of the values (a
// file written with an editor or through /boot has one). A network without
// a password that is not open was never usable and is not returned.
func (w radioControl) configuredProfile() (wifiProfile, bool) {
	ssid := readTrimmed(filepath.Join(w.etc, "wifi.ssid"))
	if ssid == "" {
		return wifiProfile{}, false
	}
	p := wifiProfile{Ssid: ssid, Password: strings.TrimRight(readTrimmed(filepath.Join(w.etc, "wifi.pass")), "\r"),
		Security: readTrimmed(filepath.Join(w.etc, "wifi.security")),
		Hidden:   readTrimmed(filepath.Join(w.etc, "wifi.hidden")) == "true"}
	if p.Security == "" {
		p.Security = "personal"
	}
	if p.Password == "" && p.Security != "open" {
		return wifiProfile{}, false
	}
	return p, true
}

func (w radioControl) setAutoConnect(ssid string, auto bool) error {
	path := w.savedPath(ssid)
	if _, err := os.Stat(path); err != nil {
		return err
	}
	value := "0"
	if auto {
		value = "1"
	}
	return writePrivateFile(filepath.Join(path, "auto"), []byte(value))
}

// selectNetwork marks ssid as chosen in this boot: S30wifi joins it even
// when it does not connect automatically. /run is empty after a reboot.
func (w radioControl) selectNetwork(ssid string) error {
	path := w.selectedPath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writePrivateFile(path, []byte(savedName(ssid)))
}

func (w radioControl) clearSelected() error {
	path := w.selectedPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// removeRunningNetwork drops the blocks of ssid from the running
// wpa_supplicant, so a forgotten network is not joined before the next start.
// Best effort: LIST_NETWORKS escapes the SSID (printf_encode), and decodeSSID
// undoes \xNN but not \" or \\; such a network stays until the next start.
func (w radioControl) removeRunningNetwork(ssid string) {
	if w.request == nil {
		return
	}
	networks, err := w.request("LIST_NETWORKS")
	if err != nil {
		return
	}
	for _, line := range strings.Split(networks, "\n")[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || decodeSSID(fields[1]) != ssid {
			continue
		}
		if reply, err := w.request("REMOVE_NETWORK " + fields[0]); err != nil || strings.TrimSpace(reply) != "OK" {
			log.Warnf("Wi-Fi: could not remove a forgotten network from wpa_supplicant: %v %s", err, reply)
		}
	}
}

// rememberJoined saves a network the user is joining and marks it chosen.
func (w radioControl) rememberJoined(p wifiProfile, auto bool) error {
	if err := w.saveNetwork(p, auto); err != nil {
		return err
	}
	return w.selectNetwork(p.Ssid)
}

func (s *Service) GetSavedWifi(c *gin.Context) {
	var rsp proto.Response
	if err := wifiControl.migrateSaved(); err != nil {
		log.Warnf("Wi-Fi: could not store the configured network as a saved one: %v", err)
	}
	networks, err := wifiControl.readSaved()
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to read saved networks")
		return
	}
	if networks == nil {
		networks = []savedNetwork{}
	}
	rsp.OkRspWithData(c, networks)
}

type savedWifiReq struct {
	Ssid string `json:"ssid"`
}

// ConnectSavedWifi joins a saved network with its stored password and keeps
// its automatic-connection setting.
func (s *Service) ConnectSavedWifi(c *gin.Context) {
	var req savedWifiReq
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || req.Ssid == "" {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	if !checkRadio(c, true) {
		return
	}
	p, auto, err := wifiControl.savedProfile(req.Ssid)
	if err != nil || p.validate() != nil {
		finishWifi(nil)
		rsp.ErrRsp(c, -1, "unknown network")
		return
	}
	freqs := wifiControl.frequencies()
	if err := wifiControl.saveProfile(p, freqs, wifiControl.preferredBand()); err != nil {
		finishWifi(err)
		rsp.ErrRsp(c, -1, "failed to save Wi-Fi")
		return
	}
	if err := wifiControl.rememberJoined(p, auto); err != nil {
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

// ForgetSavedWifi deletes a saved network. Forgetting the current one leaves
// it; Wi-Fi then joins another saved network that connects automatically.
func (s *Service) ForgetSavedWifi(c *gin.Context) {
	var req savedWifiReq
	var rsp proto.Response
	if c.ShouldBindJSON(&req) != nil || req.Ssid == "" {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	if !reserveWifi() {
		rsp.ErrRsp(c, -2, "Wi-Fi busy")
		return
	}
	current := readTrimmed(filepath.Join(wifiControl.etc, "wifi.ssid")) == req.Ssid
	if err := os.RemoveAll(wifiControl.savedPath(req.Ssid)); err != nil {
		finishWifi(err)
		rsp.ErrRsp(c, -1, "failed to forget the network")
		return
	}
	if !current {
		wifiControl.removeRunningNetwork(req.Ssid)
		finishWifi(nil)
		rsp.OkRsp(c)
		return
	}
	rsp.OkRsp(c)
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		err := errors.Join(wifiControl.forgetProfile(), wifiControl.clearSelected())
		if err == nil && wifiControl.present() && !isAPMode() {
			if _, err = wifiControl.run(WiFiScript, "disable"); err == nil {
				_, err = wifiControl.run(WiFiScript, "start")
			}
		}
		finishWifi(err)
	}()
}
