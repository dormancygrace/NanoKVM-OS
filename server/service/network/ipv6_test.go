package network

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func ipv6Fixture(t *testing.T) {
	t.Helper()
	previousConfig, previousRoot := ipv6ConfigFile, ipv6SysctlRoot
	t.Cleanup(func() { ipv6ConfigFile, ipv6SysctlRoot = previousConfig, previousRoot })
	root := t.TempDir()
	ipv6ConfigFile = filepath.Join(root, "settings/ipv6")
	ipv6SysctlRoot = filepath.Join(root, "proc")
	for _, name := range []string{"all", "default", "lo", "eth0", "wlan0", "usb0"} {
		path := filepath.Join(ipv6SysctlRoot, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "disable_ipv6"), []byte("0\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertIPv6State(t *testing.T, disabled string) {
	t.Helper()
	for _, name := range []string{"all", "default", "lo", "eth0", "wlan0", "usb0"} {
		want := disabled
		if name == "lo" {
			want = "0"
		}
		got, err := os.ReadFile(filepath.Join(ipv6SysctlRoot, name, "disable_ipv6"))
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Fatalf("%s: %q %v; want %s", name, got, err, want)
		}
	}
}

func TestIPv6DefaultsAndPersistentTransitions(t *testing.T) {
	ipv6Fixture(t)
	enabled, err := readIPv6Preference()
	if err != nil || enabled {
		t.Fatalf("missing config: %t %v", enabled, err)
	}
	for _, enabled := range []bool{false, true, false} {
		if err := applyIPv6(enabled); err != nil {
			t.Fatal(err)
		}
		if err := writeIPv6Preference(enabled); err != nil {
			t.Fatal(err)
		}
		got, err := readIPv6Preference()
		if err != nil || got != enabled {
			t.Fatalf("persistent preference: %t %v", got, err)
		}
		// Reapplying an unchanged policy must leave the interface state intact.
		if err := applyIPv6(enabled); err != nil {
			t.Fatal(err)
		}
		disabled := "1"
		if enabled {
			disabled = "0"
		}
		assertIPv6State(t, disabled)
	}
}

func TestIPv6RejectsMissingOrInvalidSwitchWithoutMutation(t *testing.T) {
	ipv6Fixture(t)
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":1}`} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest("POST", "/api/network/ipv6", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetIPv6(ctx)
		if !strings.Contains(response.Body.String(), `"code":-1`) {
			t.Fatalf("%s: %s", body, response.Body.String())
		}
		assertIPv6State(t, "0")
	}
	if _, err := os.Stat(ipv6ConfigFile); !os.IsNotExist(err) {
		t.Fatal("invalid requests wrote configuration")
	}
}

func TestIPv6RejectsCorruptConfigAndReportsUnsupportedKernel(t *testing.T) {
	ipv6Fixture(t)
	if err := writeIPv6Preference(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ipv6ConfigFile, []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIPv6Preference(); err == nil {
		t.Fatal("corrupt config accepted")
	}
	ipv6SysctlRoot = filepath.Join(t.TempDir(), "no-ipv6")
	if err := applyIPv6(true); err == nil {
		t.Fatal("enabled unsupported IPv6")
	}
	if err := applyIPv6(false); err != nil {
		t.Fatal(err)
	}
}
