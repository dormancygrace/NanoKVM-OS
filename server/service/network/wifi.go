package network

import (
	"crypto/subtle"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	WiFiExistFile  = "/etc/kvm/wifi_exist"
	WiFiApModeFile = "/tmp/wifiap"
	WiFiSSID       = "/etc/kvm/wifi.ssid"
	WiFiPasswd     = "/etc/kvm/wifi.pass"
	WiFiConnect    = "/kvmapp/kvm/wifi_try_connect"
	WiFiStateFile  = "/run/nanokvm/wifi_state"
	WiFiScript     = "/etc/init.d/S30wifi"
	WiFiApPassFile = "/kvmapp/kvm/ap.pass"
)

func (s *Service) GetWifi(c *gin.Context) {
	var rsp proto.Response

	status := wifiControl.status()
	status.ThisConnection = requestOnInterface(c, "wlan0")
	rsp.OkRspWithData(c, status)
}

// The two routes without a login act only during AP provisioning, only for
// a client of the device's own access point (a connection that arrived on
// wlan0, whatever address it was sent to), and only with the AP key the
// OLED shows. That key is also the access point's WPA passphrase;
// failures are counted per key, and after apKeyMaxFailures the routes stay
// shut until kvm_system starts provisioning again with a new key.
const apKeyMaxFailures = 10

var (
	apPenalty = 2 * time.Second
	apMode    = func() bool { return isSupported() && isAPMode() }
	// An address check alone lets a host on Ethernet route the AP subnet
	// through the device; the ingress interface is what the kernel saw.
	apAvailable = func(c *gin.Context) bool {
		if !apMode() {
			return false
		}
		ingress, err := utils.IngressInterface(c.Request)
		return err == nil && ingress == "wlan0"
	}
	apPassword = getApPassword
	apGuard    apKeyGuard
)

type apKeyGuard struct {
	mu       sync.Mutex
	key      string
	failures int
}

// allow compares given with the current key under the lock, so parallel
// guesses are counted, not raced.
func (g *apKeyGuard) allow(given, current string) (ok, locked bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if current != g.key {
		g.key, g.failures = current, 0
	}
	if g.failures >= apKeyMaxFailures {
		return false, true
	}
	if given != "" && current != "" && subtle.ConstantTimeCompare([]byte(given), []byte(current)) == 1 {
		return true, false
	}
	g.failures++
	if g.failures < apKeyMaxFailures {
		return false, false
	}
	// Once per key: refused requests after this are not logged.
	log.Warnf("Wi-Fi provisioning: %d wrong AP keys; refused until provisioning restarts", apKeyMaxFailures)
	return false, true
}

// checkApRequest answers a request that may not act and reports whether it
// may.
func checkApRequest(c *gin.Context) bool {
	var rsp proto.Response
	if !apAvailable(c) {
		time.Sleep(apPenalty)
		rsp.ErrRsp(c, -1, "invalid mode")
		return false
	}
	ok, locked := apGuard.allow(c.GetHeader("X-AP-Key"), apPassword())
	if locked {
		time.Sleep(apPenalty)
		rsp.ErrRsp(c, -5, "too many attempts: restart Wi-Fi provisioning on the device")
		return false
	}
	if !ok {
		time.Sleep(apPenalty)
		rsp.ErrRsp(c, -4, "unauthorized")
		return false
	}
	return true
}

func (s *Service) ConnectWifiNoAuth(c *gin.Context) {
	var req proto.ConnectWifiReq
	var rsp proto.Response

	if !checkApRequest(c) {
		return
	}

	if err := proto.ParseFormRequest(c, &req); err != nil {
		time.Sleep(1 * time.Second)
		rsp.ErrRsp(c, -2, "invalid parameters")
		return
	}

	if legacyProfile(req.Ssid, req.Password).validate() != nil {
		rsp.ErrRsp(c, -2, "invalid parameters")
		return
	}
	if !reserveWifi() {
		rsp.ErrRsp(c, -2, "Wi-Fi busy")
		return
	}
	defer finishWifi(nil)
	if err := connect(req.Ssid, req.Password); err != nil {
		rsp.ErrRsp(c, -3, "failed to connect wifi")
		return
	}

	time.Sleep(5 * time.Second)

	rsp.OkRsp(c)
	log.Debugf("set wifi ap mode successfully")
}

func (s *Service) VerifyApLogin(c *gin.Context) {
	var rsp proto.Response

	if !checkApRequest(c) {
		return
	}

	rsp.OkRsp(c)
}

func (s *Service) ConnectWifi(c *gin.Context) {
	var req proto.ConnectWifiReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil || legacyProfile(req.Ssid, req.Password).validate() != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}

	if !reserveWifi() {
		rsp.ErrRsp(c, -2, "Wi-Fi busy")
		return
	}
	defer finishWifi(nil)
	if err := connect(req.Ssid, req.Password); err != nil {
		rsp.ErrRsp(c, -2, "failed to connect wifi")
		return
	}

	// Waiting for Wi-Fi connection
	maxTries := 7
	if isConnected() {
		maxTries = 3
		time.Sleep(12 * time.Second)
	}

	for range maxTries {
		time.Sleep(2 * time.Second)

		if isConnected() && getWiFiSsid() != "" {
			rsp.OkRsp(c)
			log.Debugf("set wifi successfully: %s", req.Ssid)
			return
		}
	}

	rsp.ErrRsp(c, -3, "failed to get wifi status")
}

// legacyProfile is what the two legacy connect routes save: a personal
// network, which S30wifi cannot start with a password wpa_passphrase
// rejects (under 8 or over 63 characters, a line break).
func legacyProfile(ssid, password string) wifiProfile {
	return wifiProfile{Ssid: ssid, Password: password, Security: "personal"}
}

func connect(ssid string, password string) error {
	// Legacy/AP provisioning selects automatic bands and personal security.
	for _, name := range []string{"wifi.disabled", "wifi.security", "wifi.hidden", "wifi.freq_list"} {
		if err := os.Remove(filepath.Join("/etc/kvm", name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	if err := writePrivateFile(WiFiSSID, []byte(ssid)); err != nil {
		log.Errorf("failed to save wifi ssid: %s", err)
		return err
	}

	if err := writePrivateFile(WiFiPasswd, []byte(password)); err != nil {
		log.Errorf("failed to save wifi password: %s", err)
		return err
	}

	if err := os.WriteFile(WiFiConnect, nil, 0o644); err != nil {
		log.Errorf("failed to connect wifi: %s", err)
		return err
	}

	return nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}

	// WriteFile preserves the mode of an existing file, so explicitly tighten
	// credentials created by older releases with mode 0644.
	return os.Chmod(path, 0o600)
}

func isSupported() bool {
	_, err := os.Stat("/sys/class/net/wlan0")
	return err == nil
}

func isAPMode() bool {
	_, err := os.Stat(WiFiApModeFile)
	return err == nil
}

func isConnected() bool {
	content, err := os.ReadFile(WiFiStateFile)
	if err != nil {
		return false
	}

	state := strings.ReplaceAll(string(content), "\n", "")
	return state == "1"
}

func getWiFiSsid() string {
	ssidByte, err := os.ReadFile(WiFiSSID)
	if err != nil {
		return ""
	}

	return strings.ReplaceAll(string(ssidByte), "\n", "")
}

func getApPassword() string {
	passByte, err := os.ReadFile(WiFiApPassFile)
	if err != nil {
		return ""
	}

	return strings.ReplaceAll(string(passByte), "\n", "")
}
