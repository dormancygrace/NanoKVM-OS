package network

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWolInterfacesAndSelection(t *testing.T) {
	original := systemNetworkInterfaces
	originalSysClassNet := wolSysClassNet
	wolSysClassNet = t.TempDir()
	for _, item := range []struct {
		name   string
		uevent string
	}{
		{name: "eth0", uevent: "INTERFACE=eth0\nIFINDEX=2\n"},
		{name: "wlan0", uevent: "DEVTYPE=wlan\nINTERFACE=wlan0\nIFINDEX=4\n"},
		{name: "usb0", uevent: "DEVTYPE=gadget\nINTERFACE=usb0\nIFINDEX=7\n"},
	} {
		base := filepath.Join(wolSysClassNet, item.name)
		if err := os.MkdirAll(filepath.Join(base, "device"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "uevent"), []byte(item.uevent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	systemNetworkInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Name: "wlan0", HardwareAddr: net.HardwareAddr{0, 1, 2, 3, 4, 5}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
			{Name: "eth0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 11}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "usb0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 13}, Flags: net.FlagUp | net.FlagBroadcast},
			{Name: "down0", HardwareAddr: net.HardwareAddr{6, 7, 8, 9, 10, 12}, Flags: net.FlagBroadcast},
			{Name: "tun0", Flags: net.FlagUp},
		}, nil
	}
	t.Cleanup(func() {
		systemNetworkInterfaces = original
		wolSysClassNet = originalSysClassNet
	})

	interfaces, err := wolInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(interfaces, []string{"eth0", "wlan0"}) {
		t.Fatalf("interfaces = %#v", interfaces)
	}
	if selected, err := selectWolInterface(""); err != nil || selected != "eth0" {
		t.Fatalf("default interface = %q, %v", selected, err)
	}
	if selected, err := selectWolInterface("wlan0"); err != nil || selected != "wlan0" {
		t.Fatalf("selected interface = %q, %v", selected, err)
	}
	for _, invalid := range []string{"lo", "usb0", "down0", "wlan0;reboot", " wlan0 "} {
		if _, err := selectWolInterface(invalid); err == nil && invalid != " wlan0 " {
			t.Errorf("accepted invalid interface %q", invalid)
		}
	}
}

func TestWolCommandUsesSeparateArguments(t *testing.T) {
	cmd := wolCommand("wlan0", "AA:BB:CC:DD:EE:FF")
	want := []string{"ether-wake", "-i", "wlan0", "-b", "AA:BB:CC:DD:EE:FF"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command argv = %#v", cmd.Args)
	}
}

func TestWolHistoryIsBoundedAndKeepsNamedEntries(t *testing.T) {
	original := WolMacFile
	WolMacFile = filepath.Join(t.TempDir(), "cache", "wol")
	defer func() { WolMacFile = original }()

	saveMac("AA:BB:CC:00:00:01")
	wolMacMutex.Lock()
	flushWolPendingLocked()
	wolMacMutex.Unlock()
	macs, err := readWolMacs()
	if err != nil || len(macs) != 1 {
		t.Fatalf("history = %v, %v", macs, err)
	}
	if err = writeWolMacs([]string{formatWolMacLine("AA:BB:CC:00:00:01", "office")}); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= maxWolHistory+20; i++ {
		saveMac(fmt.Sprintf("AA:BB:CC:00:%02X:%02X", i/256, i%256))
	}
	wolMacMutex.Lock()
	flushWolPendingLocked()
	wolMacMutex.Unlock()
	macs, err = readWolMacs()
	if err != nil || len(macs) != maxWolHistory {
		t.Fatalf("history length = %d, %v", len(macs), err)
	}
	if macs[0] != formatWolMacLine("AA:BB:CC:00:00:01", "office") {
		t.Fatalf("named entry evicted: %q", macs[0])
	}
	saveMac("AA:BB:CC:00:00:01")
	if again, _ := readWolMacs(); len(again) != maxWolHistory {
		t.Fatal("an existing address was added twice")
	}
}

func TestWolHistoryWritesAreBatched(t *testing.T) {
	original, originalInterval := WolMacFile, wolFlushInterval
	WolMacFile = filepath.Join(t.TempDir(), "cache", "wol")
	wolFlushInterval = time.Hour
	defer func() {
		wolMacMutex.Lock()
		if wolFlushTimer != nil {
			wolFlushTimer.Stop()
			wolFlushTimer = nil
		}
		wolPending = nil
		wolMacMutex.Unlock()
		WolMacFile, wolFlushInterval = original, originalInterval
	}()

	// A loop of wakes with fresh addresses writes nothing yet...
	for i := range 50 {
		saveMac(fmt.Sprintf("AA:BB:CC:01:00:%02X", i))
	}
	if _, err := os.Stat(WolMacFile); !os.IsNotExist(err) {
		t.Fatalf("history written per wake: %v", err)
	}
	// ...but the history already shows them.
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/network/wol/mac", nil)
	(&Service{}).GetMac(c)
	if got := strings.Count(w.Body.String(), "AA:BB:CC:01:00:"); got != 50 {
		t.Fatalf("history shows %d pending addresses: %s", got, w.Body.String())
	}
	if _, err := os.Stat(WolMacFile); !os.IsNotExist(err) {
		t.Fatal("reading the history wrote it")
	}
	// The timer writes them all in one go.
	wolMacMutex.Lock()
	flushWolPendingLocked()
	wolMacMutex.Unlock()
	if macs, err := readWolMacs(); err != nil || len(macs) != 50 {
		t.Fatalf("flushed history = %d, %v", len(macs), err)
	}
}

func TestWakeLimiter(t *testing.T) {
	l := &wakeLimiter{burst: 5, every: time.Second}
	now := time.Unix(1000, 0)
	for i := range 5 {
		if !l.allow(now) {
			t.Fatalf("wake %d of the burst refused", i)
		}
	}
	if l.allow(now) {
		t.Fatal("a sixth wake at once was allowed")
	}
	if !l.allow(now.Add(time.Second)) || l.allow(now.Add(time.Second)) {
		t.Fatal("one wake a second after the burst")
	}
}
