package network

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

var ipv6FixtureNames = []string{"all", "default", "lo", "eth0", "wlan0", "usb0"}

func ipv6Fixture(t *testing.T) {
	t.Helper()
	previousConfig, previousRoot := ipv6ConfigFile, ipv6SysctlRoot
	t.Cleanup(func() { ipv6ConfigFile, ipv6SysctlRoot = previousConfig, previousRoot })
	root := t.TempDir()
	ipv6ConfigFile = filepath.Join(root, "settings/ipv6")
	ipv6SysctlRoot = filepath.Join(root, "proc")
	for _, name := range ipv6FixtureNames {
		path := filepath.Join(ipv6SysctlRoot, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "disable_ipv6"), []byte("0\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func sysctlValue(t *testing.T, name string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(ipv6SysctlRoot, name, "disable_ipv6"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(got))
}

// assertIPv6 checks disable_ipv6 of every fixture interface: Ethernet and
// Wi-Fi by their own setting, lo always on, the others by "any".
func assertIPv6(t *testing.T, eth0, wlan0 bool) {
	t.Helper()
	disabled := func(on bool) string {
		if on {
			return "0"
		}
		return "1"
	}
	want := map[string]string{
		"lo": "0", "eth0": disabled(eth0), "wlan0": disabled(wlan0),
		"all": disabled(eth0 || wlan0), "default": disabled(eth0 || wlan0), "usb0": disabled(eth0 || wlan0),
	}
	for _, name := range ipv6FixtureNames {
		if got := sysctlValue(t, name); got != want[name] {
			t.Fatalf("%s: disable_ipv6 %s, want %s", name, got, want[name])
		}
	}
}

func TestIPv6DefaultsAndPersistentTransitions(t *testing.T) {
	ipv6Fixture(t)
	policy, err := readIPv6Policy()
	if err != nil || policy.any() {
		t.Fatalf("missing config: %v %v", policy, err)
	}
	for _, step := range []struct{ eth0, wlan0 bool }{{false, false}, {true, false}, {false, true}, {true, true}, {false, false}} {
		policy := ipv6Policy{"eth0": step.eth0, "wlan0": step.wlan0}
		if err := applyIPv6(policy); err != nil {
			t.Fatal(err)
		}
		if err := writeIPv6Policy(policy); err != nil {
			t.Fatal(err)
		}
		got, err := readIPv6Policy()
		if err != nil || got["eth0"] != step.eth0 || got["wlan0"] != step.wlan0 {
			t.Fatalf("persistent policy: %v %v", got, err)
		}
		// Reapplying an unchanged policy must leave the interface state intact.
		if err := applyIPv6(got); err != nil {
			t.Fatal(err)
		}
		assertIPv6(t, step.eth0, step.wlan0)
	}
}

func TestIPv6ReadsTheSettingFromBeforeInterfaces(t *testing.T) {
	ipv6Fixture(t)
	if err := os.MkdirAll(filepath.Dir(ipv6ConfigFile), 0755); err != nil {
		t.Fatal(err)
	}
	for content, both := range map[string]bool{"1\n": true, "0\n": false} {
		if err := os.WriteFile(ipv6ConfigFile, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		policy, err := readIPv6Policy()
		if err != nil || policy["eth0"] != both || policy["wlan0"] != both {
			t.Fatalf("%q: %v %v", content, policy, err)
		}
		runIPv6Script(t, "start")
		assertIPv6(t, both, both)
	}
}

func runIPv6Script(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{"-s", "--"}, args...)...)
	cmd.Stdin = strings.NewReader(ipv6BootScript)
	cmd.Env = append(os.Environ(), "NANOKVM_IPV6_SYSCTL="+ipv6SysctlRoot, "NANOKVM_IPV6_CONF="+ipv6ConfigFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("S02ipv6 %v: %v: %s", args, err, output)
	}
}

// An interface that appears later starts with "default" (the others'
// setting); udev then applies its own.
func TestIPv6AppliesTheSettingToAnInterfaceAddedLater(t *testing.T) {
	ipv6Fixture(t)
	policy := ipv6Policy{"eth0": true, "wlan0": false}
	if err := applyIPv6(policy); err != nil {
		t.Fatal(err)
	}
	if err := writeIPv6Policy(policy); err != nil {
		t.Fatal(err)
	}
	// The Wi-Fi driver loads: wlan0 inherits default (IPv6 on).
	if err := os.WriteFile(filepath.Join(ipv6SysctlRoot, "wlan0/disable_ipv6"), []byte(sysctlValue(t, "default")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if sysctlValue(t, "wlan0") != "0" {
		t.Fatal("fixture: wlan0 should start from default")
	}
	runIPv6Script(t, "interface", "wlan0")
	assertIPv6(t, true, false)
}

func TestIPv6RejectsMissingOrInvalidSwitchWithoutMutation(t *testing.T) {
	ipv6Fixture(t)
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":1}`, `{"enabled":true,"interface":"usb0"}`} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest("POST", "/api/network/ipv6", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetIPv6(ctx)
		if !strings.Contains(response.Body.String(), `"code":-1`) {
			t.Fatalf("%s: %s", body, response.Body.String())
		}
		for _, name := range ipv6FixtureNames {
			if sysctlValue(t, name) != "0" {
				t.Fatalf("%s changed %s", body, name)
			}
		}
	}
	if _, err := os.Stat(ipv6ConfigFile); !os.IsNotExist(err) {
		t.Fatal("invalid requests wrote configuration")
	}
}

func setIPv6Request(t *testing.T, body string) {
	t.Helper()
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest("POST", "/api/network/ipv6", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetIPv6(ctx)
	if !strings.Contains(response.Body.String(), `"code":0`) {
		t.Fatalf("%s: %s", body, response.Body.String())
	}
}

func TestIPv6SwitchesOneInterfaceOrBoth(t *testing.T) {
	ipv6Fixture(t)
	setIPv6Request(t, `{"enabled":true,"interface":"wlan0"}`)
	assertIPv6(t, false, true)
	setIPv6Request(t, `{"enabled":true,"interface":"eth0"}`)
	assertIPv6(t, true, true)
	setIPv6Request(t, `{"enabled":false,"interface":"wlan0"}`)
	assertIPv6(t, true, false)
	// A page from before the per-interface setting switches both.
	setIPv6Request(t, `{"enabled":false}`)
	assertIPv6(t, false, false)
	setIPv6Request(t, `{"enabled":true,"interface":"eth0"}`)
	status, err := getIPv6Status()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(status.Interfaces)
	if !status.Enabled || string(encoded) != `[{"name":"eth0","enabled":true,"active":true,"present":true},{"name":"wlan0","enabled":false,"active":false,"present":true}]` {
		t.Fatalf("status %s, enabled %t", encoded, status.Enabled)
	}
}

func TestIPv6RejectsCorruptConfigAndReportsUnsupportedKernel(t *testing.T) {
	ipv6Fixture(t)
	if err := writeIPv6Policy(ipv6Policy{"eth0": true}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"yes", "eth0=2", "usb0=1", "eth0"} {
		if err := os.WriteFile(ipv6ConfigFile, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readIPv6Policy(); err == nil {
			t.Fatalf("corrupt config %q accepted", content)
		}
	}
	ipv6SysctlRoot = filepath.Join(t.TempDir(), "no-ipv6")
	if err := applyIPv6(ipv6Policy{"wlan0": true}); err == nil {
		t.Fatal("enabled unsupported IPv6")
	}
	if err := applyIPv6(ipv6Policy{}); err != nil {
		t.Fatal(err)
	}
}
