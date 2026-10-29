package network

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fallback is a device whose last chosen network (wifi.ssid) is Office while
// wpa_supplicant has moved to the saved network Cafe by itself: the running
// configuration has Office's two band blocks and Cafe's, as S30wifi writes
// them with id_str.
type fallback struct {
	root   string
	router *gin.Engine
	// The handlers run commands in goroutines: read them through log.
	mu                sync.Mutex
	commands, removed []string
}

func (f *fallback) log() (commands, removed string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, ","), strings.Join(f.removed, ",")
}

func newFallback(t *testing.T) *fallback {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previous := wifiControl
	t.Cleanup(func() {
		wifiControl = previous
		wifiMu.Lock()
		wifiBusy = false
		wifiError = ""
		wifiMu.Unlock()
	})
	f := &fallback{root: t.TempDir()}
	net := filepath.Join(f.root, "wlan0")
	if err := os.Mkdir(net, 0o700); err != nil {
		t.Fatal(err)
	}
	blocks := []fakeBlock{{"Office", savedName("Office")}, {"Office", savedName("Office")}, {"Cafe", savedName("Cafe")}}
	status := "ssid=Cafe\nid=2\nid_str=" + savedName("Cafe") + "\nwpa_state=COMPLETED\n"
	wifiControl = radioControl{etc: f.root, net: net, runDir: filepath.Join(f.root, "run"),
		run: func(name string, args ...string) (string, error) {
			if name == WiFiScript {
				f.mu.Lock()
				f.commands = append(f.commands, strings.Join(args, " "))
				f.mu.Unlock()
			}
			return "", nil
		}}
	request := fakeSupplicant(blocks, status, &f.removed)
	wifiControl.request = func(command string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return request(command)
	}
	for _, ssid := range []string{"Office", "Cafe"} {
		if err := wifiControl.saveNetwork(wifiProfile{Ssid: ssid, Password: "password1", Security: "personal"}, true); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{"wifi.ssid": "Office", "wifi.pass": "password1"} {
		if err := writePrivateFile(filepath.Join(f.root, name), []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	f.router = gin.New()
	service := NewService()
	f.router.POST("/disconnect", service.DisconnectWifi)
	f.router.POST("/forget", service.ForgetSavedWifi)
	f.router.GET("/saved", service.GetSavedWifi)
	return f
}

func (f *fallback) call(t *testing.T, method, path, body string) json.RawMessage {
	t.Helper()
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var rsp struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rsp); err != nil || rsp.Code != 0 {
		t.Fatalf("%s %s: %s, %v", method, path, rec.Body.String(), err)
	}
	return rsp.Data
}

func waitWifiIdle(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		wifiMu.Lock()
		busy, failure := wifiBusy, wifiError
		wifiMu.Unlock()
		if !busy {
			if failure != "" {
				t.Fatalf("operation failed: %s", failure)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish")
		}
	}
}

func autoConnect(t *testing.T, ssid string) bool {
	t.Helper()
	_, auto, err := wifiControl.savedProfile(ssid)
	if err != nil {
		t.Fatal(err)
	}
	return auto
}

// Disconnect leaves the connected network, Cafe, not the last chosen one:
// Office keeps connecting automatically, and so does the network chosen in
// this boot when it is another one.
func TestDisconnectStopsJoiningTheConnectedNetwork(t *testing.T) {
	f := newFallback(t)
	if err := wifiControl.selectNetwork("Office"); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/disconnect", "")
	waitWifiIdle(t)
	if autoConnect(t, "Cafe") || !autoConnect(t, "Office") {
		t.Fatalf("auto: Cafe %v, Office %v; want false, true", autoConnect(t, "Cafe"), autoConnect(t, "Office"))
	}
	if selected, _ := os.ReadFile(filepath.Join(f.root, "run", wifiSelectedFile)); string(selected) != savedName("Office") {
		t.Fatalf("selected %q; the other network chosen in this boot was cleared", selected)
	}
	if commands, _ := f.log(); commands != "disable,start" {
		t.Fatalf("commands %v", commands)
	}
}

// Without the top-level copy of the last chosen network (forgotten), the
// connected network still stops connecting automatically.
func TestDisconnectWithoutTheLastChosenProfile(t *testing.T) {
	f := newFallback(t)
	if err := wifiControl.forgetProfile(); err != nil {
		t.Fatal(err)
	}
	if err := wifiControl.selectNetwork("Cafe"); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/disconnect", "")
	waitWifiIdle(t)
	if autoConnect(t, "Cafe") {
		t.Fatal("the connected network still connects automatically")
	}
	if _, err := os.Stat(filepath.Join(wifiControl.runDir, wifiSelectedFile)); !os.IsNotExist(err) {
		t.Fatal("the left network is still chosen for this boot")
	}
}

// Forgetting the last chosen network while connected to another one removes
// its blocks from the running wpa_supplicant and its top-level copy, and
// keeps the connection: no Wi-Fi restart.
func TestForgetInactiveNetworkKeepsTheConnection(t *testing.T) {
	f := newFallback(t)
	f.call(t, "POST", "/forget", `{"ssid":"Office"}`)
	waitWifiIdle(t)
	if commands, removed := f.log(); commands != "" || removed != "0,1" {
		t.Fatalf("commands %q (want no restart), removed blocks %q (want Office's 0,1)", commands, removed)
	}
	if _, err := os.Stat(wifiControl.savedPath("Office")); !os.IsNotExist(err) {
		t.Fatal("Office is still saved")
	}
	if _, err := os.Stat(filepath.Join(f.root, "wifi.ssid")); !os.IsNotExist(err) {
		t.Fatal("the top-level copy of the forgotten network stayed")
	}
	if !autoConnect(t, "Cafe") {
		t.Fatal("Cafe changed")
	}
}

// Forgetting the connected network answers first, then leaves it: the reply
// goes out over the link being cut. The last chosen network's files stay.
func TestForgetConnectedNetworkAnswersBeforeLeaving(t *testing.T) {
	f := newFallback(t)
	f.call(t, "POST", "/forget", `{"ssid":"Cafe"}`)
	if commands, removed := f.log(); commands != "" || removed != "" {
		t.Fatalf("left before the reply: commands %q, removed %q", commands, removed)
	}
	waitWifiIdle(t)
	if commands, _ := f.log(); commands != "disable,start" {
		t.Fatalf("commands %v", commands)
	}
	if _, err := os.Stat(wifiControl.savedPath("Cafe")); !os.IsNotExist(err) {
		t.Fatal("Cafe is still saved")
	}
	if readTrimmed(filepath.Join(f.root, "wifi.ssid")) != "Office" {
		t.Fatal("the last chosen network's files were removed")
	}
}

// The saved list marks the connected network, which the page uses to warn
// that forgetting it cuts the connection.
func TestSavedListMarksTheConnectedNetwork(t *testing.T) {
	f := newFallback(t)
	var networks []savedNetwork
	if err := json.Unmarshal(f.call(t, "GET", "/saved", ""), &networks); err != nil {
		t.Fatal(err)
	}
	if len(networks) != 2 || networks[0].Ssid != "Cafe" || !networks[0].Current || networks[1].Current {
		t.Fatalf("saved %+v", networks)
	}
}
