package network

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWifiDisconnectForgetsTheNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := wifiControl
	defer func() { wifiControl = previous; wifiMu.Lock(); wifiBusy = false; wifiError = ""; wifiMu.Unlock() }()
	root := t.TempDir()
	net := filepath.Join(root, "wlan0")
	_ = os.Mkdir(net, 0700)
	var commands []string
	var fail error
	wifiControl = radioControl{etc: root, net: net, run: func(name string, args ...string) (string, error) {
		if name == WiFiScript {
			commands = append(commands, strings.Join(args, " "))
		}
		return "", fail
	}}
	router := gin.New()
	router.POST("/disconnect", NewService().DisconnectWifi)
	post := func() int {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("POST", "/disconnect", nil))
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
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			wifiMu.Lock()
			busy := wifiBusy
			wifiMu.Unlock()
			if !busy {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("operation did not finish")
			}
		}
	}
	for _, name := range append([]string{wifiBandPreferenceFile}, wifiProfileFiles...) {
		if err := writePrivateFile(filepath.Join(root, name), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if post() != 0 {
		t.Fatal("disconnect rejected")
	}
	wait()
	for _, name := range wifiProfileFiles {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("%s kept", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, wifiBandPreferenceFile)); err != nil {
		t.Fatal("band preference removed")
	}
	if strings.Join(commands, ",") != "disable,start" || !wifiControl.enabled() {
		t.Fatalf("commands %v, enabled %v", commands, wifiControl.enabled())
	}
	wifiMu.Lock()
	if wifiError != "" {
		t.Fatalf("error %q", wifiError)
	}
	wifiMu.Unlock()

	fail = errors.New("command failed")
	if post() != 0 {
		t.Fatal("disconnect not scheduled")
	}
	wait()
	wifiMu.Lock()
	operationError := wifiError
	wifiMu.Unlock()
	if operationError == "" {
		t.Fatal("async error lost")
	}

	_ = writePrivateFile(filepath.Join(root, "wifi.disabled"), nil)
	if post() == 0 {
		t.Fatal("disconnect accepted while Wi-Fi is off")
	}
}

// A real connection to the loopback address ends on "lo", not on another interface.
func TestRequestOnInterfaceOverLoopback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var onLo, onOther bool
	router.GET("/", func(c *gin.Context) {
		onLo, onOther = requestOnInterface(c, "lo"), requestOnInterface(c, "absent0")
	})
	server := httptest.NewServer(router)
	defer server.Close()
	rsp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	rsp.Body.Close()
	if !onLo || onOther {
		t.Fatalf("lo %v, absent %v", onLo, onOther)
	}
}

func TestRequestOnInterfaceWithoutLocalAddress(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	if requestOnInterface(c, "lo") {
		t.Fatal("request without a local address reported on an interface")
	}
}
