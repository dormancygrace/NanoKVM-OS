package network

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"NanoKVM-Server/internal/networktrial"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The access point the administrator turns on (Network > Wi-Fi > Access
// point): Wi-Fi becomes a network of its own instead of joining one, until
// it is turned off. One file per setting in /etc/kvm, read by S30wifi
// (ap_start user). The setup access point of the device's button
// (kvm_system, /tmp/wifiap, /kvmapp/kvm/ap.*) is separate: only it opens the
// unauthenticated setup page.
var (
	wifiAPPrefix  = "/etc/kvm/wifi.ap"
	wifiAPRunning = "/run/nanokvm-wifi/ap-user"
	wifiAPLeases  = "/run/nanokvm-wifi/udhcpd.leases"
	wifiAPUIDFile = "/sys/class/cvi-base/base_uid"
)

func wifiAPFile(name string) string { return wifiAPPrefix + "." + name }

// The files a change writes, which a trial restores.
func wifiAPFiles() []string {
	var files []string
	for _, name := range []string{"enabled", "ssid", "pass", "band", "channel", "hidden", "subnet", "internet", "dns"} {
		files = append(files, wifiAPFile(name))
	}
	return files
}

// Channels without radar detection, which the driver offers on the boards.
var wifiAPChannels = map[string][]int{
	"2.4": {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13},
	"5":   {36, 40, 44, 48},
}

func readWifiAPFile(name string) string {
	contents, err := os.ReadFile(wifiAPFile(name))
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(contents), "\n")
}

func readWifiAP() (proto.WifiAP, bool) {
	ap := proto.WifiAP{
		SSID:   readWifiAPFile("ssid"),
		Band:   readWifiAPFile("band"),
		Subnet: readWifiAPFile("subnet"),
	}
	_, err := os.Stat(wifiAPFile("enabled"))
	ap.Enabled = err == nil
	_, err = os.Stat(wifiAPFile("hidden"))
	ap.Hidden = err == nil
	_, err = os.Stat(wifiAPFile("internet"))
	ap.Internet = err == nil
	ap.DNS = strings.Fields(readWifiAPFile("dns"))
	ap.Channel, _ = strconv.Atoi(readWifiAPFile("channel"))
	if ap.Band != "5" {
		ap.Band = "2.4"
	}
	if ap.SSID == "" {
		ap.SSID = "NanoKVM"
	}
	return ap, readWifiAPFile("pass") != ""
}

// wifiAPActive: the administrator's access point is up.
func wifiAPActive() bool {
	_, err := os.Stat(wifiAPRunning)
	return err == nil
}

// wifiIsAccessPoint: Wi-Fi serves a network, the setup one or the
// administrator's, and joins none.
func wifiIsAccessPoint() bool { return isAPMode() || wifiAPActive() }

func validWifiAPPassword(password string) bool {
	if len(password) < 8 || len(password) > 63 || strings.TrimSpace(password) != password {
		return false
	}
	for i := 0; i < len(password); i++ {
		if password[i] < 0x20 || password[i] > 0x7e {
			return false
		}
	}
	return true
}

// validWifiAPSubnet: three octets of a private /24 that is not a network an
// interface is on already.
func validWifiAPSubnet(subnet string) error {
	if subnet == "" {
		return nil
	}
	prefix, err := netip.ParsePrefix(subnet + ".0/24")
	if err != nil || strings.Count(subnet, ".") != 2 || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
		return errors.New("the network address must be three numbers of a private network, such as 192.168.50")
	}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			if iface.Name == "wlan0" {
				continue
			}
			addresses, _ := iface.Addrs()
			for _, address := range addresses {
				if ipNet, ok := address.(*net.IPNet); ok {
					if other, ok := netip.AddrFromSlice(ipNet.IP); ok && prefix.Contains(other.Unmap()) {
						return fmt.Errorf("the network %s.0/24 is already used by %s", subnet, iface.Name)
					}
				}
			}
		}
	}
	return nil
}

func validateWifiAP(ap proto.WifiAP, keepPassword bool) error {
	if len(ap.SSID) < 1 || len(ap.SSID) > 32 || !utf8.ValidString(ap.SSID) || strings.ContainsAny(ap.SSID, "\x00\r\n") {
		return errors.New("the network name must be 1 to 32 bytes")
	}
	if !keepPassword && !validWifiAPPassword(ap.Password) {
		return errors.New("the password must be 8 to 63 characters: letters, digits or symbols")
	}
	channels, ok := wifiAPChannels[ap.Band]
	if !ok {
		return errors.New("the band must be 2.4 or 5 GHz")
	}
	valid := ap.Channel == 0
	for _, channel := range channels {
		valid = valid || channel == ap.Channel
	}
	if !valid {
		return fmt.Errorf("channel %d is not offered on %s GHz", ap.Channel, ap.Band)
	}
	if len(ap.DNS) > 3 {
		return errors.New("at most three DNS servers")
	}
	for _, server := range ap.DNS {
		if addr, err := netip.ParseAddr(server); err != nil || !addr.Is4() || addr.IsUnspecified() {
			return fmt.Errorf("%q is not an IPv4 address", server)
		}
	}
	return validWifiAPSubnet(ap.Subnet)
}

func writeWifiAP(ap proto.WifiAP, keepPassword bool) error {
	write := func(name, value string) error {
		return writeFileAtomic(wifiAPFile(name), []byte(value+"\n"))
	}
	flag := func(name string, on bool) error {
		if on {
			return writePrivateFile(wifiAPFile(name), nil)
		}
		if err := os.Remove(wifiAPFile(name)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(wifiAPPrefix), 0o755); err != nil {
		return err
	}
	if err := write("ssid", ap.SSID); err != nil {
		return err
	}
	if !keepPassword {
		if err := write("pass", ap.Password); err != nil {
			return err
		}
	}
	if err := write("band", ap.Band); err != nil {
		return err
	}
	if err := write("channel", strconv.Itoa(ap.Channel)); err != nil {
		return err
	}
	if ap.Subnet == "" {
		if err := os.Remove(wifiAPFile("subnet")); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := write("subnet", ap.Subnet); err != nil {
		return err
	}
	if err := flag("hidden", ap.Hidden); err != nil {
		return err
	}
	if err := flag("internet", ap.Internet); err != nil {
		return err
	}
	if len(ap.DNS) == 0 {
		if err := os.Remove(wifiAPFile("dns")); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := write("dns", strings.Join(ap.DNS, " ")); err != nil {
		return err
	}
	return flag("enabled", ap.Enabled)
}

// wifiAPAddress is the device's address on its access point: .1 of the
// subnet, or of the one S30wifi derives from the board's serial number.
func wifiAPAddress(subnet string) string {
	if subnet != "" {
		return subnet + ".1"
	}
	uid, err := os.ReadFile(wifiAPUIDFile)
	if err != nil {
		return ""
	}
	sum := sha512.Sum512(uid)
	id2, id3 := int(sum[0]), int(sum[1])
	if id2 == id3 {
		id2++
	}
	if id2 >= 255 {
		id2 = 253
	}
	if id3 >= 255 {
		id3 = 254
	}
	return fmt.Sprintf("10.%d.%d.1", id3, id2)
}

// wifiAPStations lists the devices associated with the access point, with
// the address and name of their udhcpd lease (written every few seconds,
// see S30wifi) or else the neighbour table.
func wifiAPStations() []proto.WifiAPStation {
	stations := []proto.WifiAPStation{}
	// hostapd, not iw: it counts the time since association, which the
	// driver does not report.
	out, err := exec.Command("hostapd_cli", "-p", "/var/run/hostapd", "-i", "wlan0", "all_sta").Output()
	if err != nil {
		return stations
	}
	stations = parseAllSta(string(out))
	if len(stations) == 0 {
		return stations
	}
	leases := map[string][2]string{}
	if out, err := exec.Command("dumpleases", "-f", wifiAPLeases).Output(); err == nil {
		for mac, lease := range parseDumpLeases(string(out)) {
			leases[mac] = lease
		}
	}
	if out, err := exec.Command("ip", "-4", "neigh", "show", "dev", "wlan0").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			// 10.1.2.100 lladdr aa:bb:cc:dd:ee:ff REACHABLE
			if len(f) >= 3 && f[1] == "lladdr" {
				mac := strings.ToLower(f[2])
				if lease, ok := leases[mac]; !ok || lease[0] == "" {
					leases[mac] = [2]string{f[0], lease[1]}
				}
			}
		}
	}
	for i := range stations {
		lease := leases[stations[i].MAC]
		stations[i].IP, stations[i].Hostname = lease[0], lease[1]
	}
	return stations
}

// parseAllSta reads hostapd_cli all_sta: per station its address on a
// line of its own, then key=value lines.
func parseAllSta(out string) []proto.WifiAPStation {
	stations := []proto.WifiAPStation{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if _, err := net.ParseMAC(line); err == nil {
			stations = append(stations, proto.WifiAPStation{MAC: strings.ToLower(line)})
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || len(stations) == 0 {
			continue
		}
		st := &stations[len(stations)-1]
		switch key {
		case "signal":
			st.Signal, _ = strconv.Atoi(value)
		case "connected_time":
			st.Connected, _ = strconv.Atoi(value)
		case "tx_rate_info":
			// In units of 100 kbit/s: "650 mcs 7".
			if f := strings.Fields(value); len(f) > 0 {
				if rate, err := strconv.Atoi(f[0]); err == nil {
					st.Rate = float64(rate) / 10
				}
			}
		}
	}
	return stations
}

// parseDumpLeases reads busybox dumpleases: a header, then
// "MAC IP [hostname] expires" per lease.
func parseDumpLeases(out string) map[string][2]string {
	leases := map[string][2]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if _, err := net.ParseMAC(f[0]); err != nil || net.ParseIP(f[1]) == nil {
			continue
		}
		host := ""
		if len(f) >= 4 {
			host = f[2]
		}
		leases[strings.ToLower(f[0])] = [2]string{f[1], host}
	}
	return leases
}

func (s *Service) GetWifiAP(c *gin.Context) {
	var rsp proto.Response
	ap, passwordSet := readWifiAP()
	state := proto.WifiAPRsp{WifiAP: ap, PasswordSet: passwordSet, Running: wifiAPActive(), Address: wifiAPAddress(ap.Subnet), Stations: []proto.WifiAPStation{}}
	if state.Running {
		state.Stations = wifiAPStations()
	}
	rsp.OkRspWithData(c, &state)
}

func (s *Service) SetWifiAP(c *gin.Context) {
	var req proto.WifiAP
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}
	_, passwordSet := readWifiAP()
	keepPassword := req.Password == "" && passwordSet
	if err := validateWifiAP(req, keepPassword); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if !wifiControl.hardware() || isAPMode() {
		rsp.ErrRsp(c, -1, "Wi-Fi unavailable")
		return
	}
	if !reserveWifi() {
		rsp.ErrRsp(c, -2, "Wi-Fi busy")
		return
	}
	// Over Wi-Fi, turning the access point on or off can cut this page off:
	// on trial, as the Ethernet settings (trial.go). Over another interface
	// the page stays, and there is nothing to confirm.
	if !requestOnInterface(c, "wlan0") {
		if err := writeWifiAP(req, keepPassword); err != nil {
			log.Errorf("failed to write the Wi-Fi access point settings: %s", err)
			finishWifi(err)
			rsp.ErrRsp(c, -2, "failed to save the access point settings")
			return
		}
		_ = exec.Command("sync").Run()
		rsp.OkRspWithData(c, gin.H{})
		c.Writer.Flush()
		releaseRestart := networktrial.Begin()
		go func() {
			defer releaseRestart()
			time.Sleep(restartDelay)
			finishWifi(restartWifiAP())
		}()
		log.Infof("set the Wi-Fi access point: enabled=%t band=%s channel=%d hidden=%t internet=%t", req.Enabled, req.Band, req.Channel, req.Hidden, req.Internet)
		return
	}
	trial, err := beginTrial("wifi-ap", wifiAPFiles())
	if err != nil {
		finishWifi(nil)
		rsp.ErrRsp(c, -3, err.Error())
		return
	}
	if err := writeWifiAP(req, keepPassword); err != nil {
		log.Errorf("failed to write the Wi-Fi access point settings: %s", err)
		trial.abandon()
		finishWifi(err)
		rsp.ErrRsp(c, -2, "failed to save the access point settings")
		return
	}
	_ = exec.Command("sync").Run()
	trial.start()
	newAddress := ""
	if req.Enabled {
		newAddress = wifiAPAddress(req.Subnet)
	}
	rsp.OkRspWithData(c, gin.H{"trial": trial.info(trialHosts(newAddress, c.Request.Host))})
	c.Writer.Flush()
	releaseRestart := networktrial.Begin()
	go func() {
		defer releaseRestart()
		time.Sleep(restartDelay)
		err := restartWifiAP()
		trial.restarted(err)
		finishWifi(err)
	}()
	log.Infof("set the Wi-Fi access point on trial: enabled=%t band=%s channel=%d hidden=%t internet=%t", req.Enabled, req.Band, req.Channel, req.Hidden, req.Internet)
}

// restartWifiAP applies the settings: S30wifi starts the access point or
// joins networks again. With Wi-Fi off they wait for it to be turned on.
func restartWifiAP() error {
	if !wifiControl.enabled() {
		return nil
	}
	_, err := wifiControl.run(WiFiScript, "restart")
	return err
}
