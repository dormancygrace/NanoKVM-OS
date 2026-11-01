package network

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	networks, err := w.readSaved("")
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
// S30wifi leaves it out. current is the saved name (hex of the SSID) of the
// network wlan0 is connected to, or "".
func (w radioControl) readSaved(current string) ([]savedNetwork, error) {
	dir := filepath.Join(w.etc, wifiSavedDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
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
			Current:     current != "" && entry.Name() == current}
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

// clearSelectedIf clears the network chosen in this boot when it is name (a
// saved name); another network chosen in this boot stays chosen.
func (w radioControl) clearSelectedIf(name string) error {
	path := w.selectedPath()
	if path == "" {
		return nil
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != name {
		return nil
	}
	return w.clearSelected()
}

// validSavedName reports whether name is the saved name of an SSID: lower-case
// hex of at least one byte, as S30wifi and savedName write it.
func validSavedName(name string) bool {
	b, err := hex.DecodeString(name)
	return err == nil && len(b) > 0 && savedName(string(b)) == name
}

// wpaUnescape undoes wpa_supplicant's printf_encode, which STATUS and
// LIST_NETWORKS apply to SSIDs: \\, \", \e, \n, \r, \t and \xNN for any other
// byte outside printable ASCII. A malformed escape is not an SSID it wrote.
func wpaUnescape(value string) (string, bool) {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out = append(out, value[i])
			continue
		}
		if i+1 >= len(value) {
			return "", false
		}
		i++
		switch value[i] {
		case '\\', '"':
			out = append(out, value[i])
		case 'e':
			out = append(out, 0x1b)
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'x':
			if i+2 >= len(value) {
				return "", false
			}
			b, err := strconv.ParseUint(value[i+1:i+3], 16, 8)
			if err != nil {
				return "", false
			}
			out = append(out, byte(b))
			i += 2
		default:
			return "", false
		}
	}
	return string(out), true
}

// associated is the saved name (hex of the SSID) of the network wlan0 is
// connected to. It can differ from the last chosen network (wifi.ssid):
// wpa_supplicant moves to another saved network by itself. S30wifi names
// every block in id_str; a block of an older S30wifi is named by its SSID.
func (w radioControl) associated() (string, bool) {
	if w.request == nil {
		return "", false
	}
	out, err := w.request("STATUS")
	if err != nil {
		return "", false
	}
	var state, ssid, idStr string
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), "=")
		if !ok {
			continue
		}
		switch key {
		case "wpa_state":
			state = value
		case "ssid":
			ssid = value
		case "id_str":
			idStr = value
		}
	}
	if state != "COMPLETED" {
		return "", false
	}
	if validSavedName(idStr) {
		return idStr, true
	}
	if decoded, ok := wpaUnescape(ssid); ok && decoded != "" {
		return savedName(decoded), true
	}
	return "", false
}

// runningName is the saved name of block id in the running wpa_supplicant:
// its id_str, or its SSID as LIST_NETWORKS shows it for a block without one.
func (w radioControl) runningName(id, listed string) (string, bool) {
	if reply, err := w.request("GET_NETWORK " + id + " id_str"); err == nil {
		value := strings.TrimSpace(reply)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			name := value[1 : len(value)-1]
			return name, validSavedName(name)
		}
	}
	if decoded, ok := wpaUnescape(listed); ok && decoded != "" {
		return savedName(decoded), true
	}
	return "", false
}

// removeRunningNetwork drops the blocks of ssid from the running
// wpa_supplicant, so a forgotten network is not joined before the next start.
// Blocks are matched by id_str, or by the unescaped SSID; never by a partly
// decoded one (Cafe\A and Cafe\x41 are different networks).
func (w radioControl) removeRunningNetwork(ssid string) {
	if w.request == nil {
		return
	}
	networks, err := w.request("LIST_NETWORKS")
	if err != nil {
		return
	}
	name := savedName(ssid)
	for _, line := range strings.Split(networks, "\n")[1:] {
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.ParseUint(fields[0], 10, 31); err != nil {
			continue
		}
		if running, ok := w.runningName(fields[0], fields[1]); !ok || running != name {
			continue
		}
		if reply, err := w.request("REMOVE_NETWORK " + fields[0]); err != nil || strings.TrimSpace(reply) != "OK" {
			log.Warnf("Wi-Fi: could not remove a forgotten network from wpa_supplicant: %v %s", err, reply)
		}
	}
}

// stopJoiningAssociated turns automatic connection off for the network wlan0
// is connected to, which Disconnect leaves. Other saved networks keep theirs.
func (w radioControl) stopJoiningAssociated() error {
	name, ok := w.associated()
	if !ok {
		return nil
	}
	if err := w.clearSelectedIf(name); err != nil {
		return err
	}
	raw, _ := hex.DecodeString(name)
	err := w.setAutoConnect(string(raw), false)
	if !os.IsNotExist(err) {
		return err
	}
	// Not saved yet: the network of a release with one network. Without an
	// entry S30wifi would store it again as one that connects automatically
	// and rejoin it at once.
	if p, ok := w.configuredProfile(); ok && savedName(p.Ssid) == name {
		return w.saveNetwork(p, false)
	}
	return nil
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
	current, _ := wifiControl.associated()
	networks, err := wifiControl.readSaved(current)
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

// ForgetSavedWifi deletes a saved network. Forgetting the network wlan0 is
// connected to leaves it, after the reply; Wi-Fi then joins another saved
// network that connects automatically. Any other one is only removed from
// the running wpa_supplicant: the connection stays.
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
	name := savedName(req.Ssid)
	associated, _ := wifiControl.associated()
	// The last chosen network (wifi.ssid) is not necessarily the connected
	// one; its top-level copy goes with it either way.
	lastChosen := readTrimmed(filepath.Join(wifiControl.etc, "wifi.ssid")) == req.Ssid
	err := os.RemoveAll(wifiControl.savedPath(req.Ssid))
	if err == nil && lastChosen {
		err = wifiControl.forgetProfile()
	}
	if err == nil {
		err = wifiControl.clearSelectedIf(name)
	}
	if err != nil {
		finishWifi(err)
		rsp.ErrRsp(c, -1, "failed to forget the network")
		return
	}
	if associated != name {
		wifiControl.removeRunningNetwork(req.Ssid)
		finishWifi(nil)
		rsp.OkRsp(c)
		return
	}
	rsp.OkRsp(c)
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		var err error
		// disable also drops the addresses of the left network.
		if wifiControl.present() && !wifiIsAccessPoint() {
			if _, err = wifiControl.run(WiFiScript, "disable"); err == nil {
				_, err = wifiControl.run(WiFiScript, "start")
			}
		}
		finishWifi(err)
	}()
}
