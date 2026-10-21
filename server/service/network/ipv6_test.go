package network

import (
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

var ipv6FixtureNames = []string{"all", "default", "lo", "eth0", "wlan0", "usb0"}

func dhcpv6If(on bool) ipv6Setting {
	if on {
		return ipv6Setting{Mode: ipv6DHCPv6}
	}
	return ipv6Setting{}
}

// stubCommand writes a command that logs its arguments to <dir>/<name>.log.
func stubCommand(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\necho \"$*\" >> " + path + ".log\n"
	if name == "ip" {
		script = "#!/bin/sh\nif [ \"$1\" = -o ]; then echo '2: eth0: <BROADCAST,UP,LOWER_UP>'; exit 0; fi\n" + strings.TrimPrefix(script, "#!/bin/sh\n")
	}
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func ipv6Fixture(t *testing.T) {
	t.Helper()
	previousConfig, previousRoot := ipv6ConfigFile, ipv6SysctlRoot
	t.Cleanup(func() { ipv6ConfigFile, ipv6SysctlRoot = previousConfig, previousRoot })
	root := t.TempDir()
	ipv6ConfigFile = filepath.Join(root, "settings/ipv6")
	ipv6SysctlRoot = filepath.Join(root, "proc")
	// The script must never touch the host's network in a test.
	run := filepath.Join(root, "run")
	if err := os.MkdirAll(run, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NANOKVM_IPV6_RUN", run)
	t.Setenv("NANOKVM_DHCPV6_RUN", run)
	t.Setenv("NANOKVM_IP", stubCommand(t, root, "ip"))
	t.Setenv("NANOKVM_DHCPV6", stubCommand(t, root, "dhcpv6"))
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
		policy := ipv6Policy{"eth0": dhcpv6If(step.eth0), "wlan0": dhcpv6If(step.wlan0)}
		if err := applyIPv6(policy); err != nil {
			t.Fatal(err)
		}
		if err := writeIPv6Policy(policy); err != nil {
			t.Fatal(err)
		}
		got, err := readIPv6Policy()
		if err != nil || got["eth0"] != dhcpv6If(step.eth0) || got["wlan0"] != dhcpv6If(step.wlan0) {
			t.Fatalf("persistent policy: %v %v", got, err)
		}
		// Reapplying an unchanged policy must leave the interface state intact.
		if err := applyIPv6(got); err != nil {
			t.Fatal(err)
		}
		assertIPv6(t, step.eth0, step.wlan0)
	}
}

// With IPv6 off and nothing to undo, the server sets the values itself; the
// result is the script's, byte for byte, from any starting state.
func TestIPv6OffWithoutTheScriptMatchesTheScript(t *testing.T) {
	snapshot := func() map[string]string {
		out := map[string]string{}
		_ = filepath.WalkDir(ipv6SysctlRoot, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				data, _ := os.ReadFile(path)
				out[strings.TrimPrefix(path, ipv6SysctlRoot)] = string(data)
			}
			return nil
		})
		return out
	}
	prepare := func(start string) {
		for _, name := range ipv6FixtureNames {
			for _, key := range []string{"disable_ipv6", "autoconf", "keep_addr_on_down"} {
				if err := os.WriteFile(filepath.Join(ipv6SysctlRoot, name, key), []byte(start+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, start := range []string{"0", "1"} {
		ipv6Fixture(t)
		prepare(start)
		runIPv6Script(t, "start")
		want := snapshot()

		ipv6Fixture(t)
		prepare(start)
		done, err := applyIPv6Off()
		if err != nil || !done {
			t.Fatalf("fast path: %v %v", done, err)
		}
		if got := snapshot(); len(got) != len(want) {
			t.Fatalf("start %s: %v, script %v", start, got, want)
		} else {
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("start %s: %s = %q, script %q", start, k, got[k], v)
				}
			}
		}
		assertIPv6(t, false, false)
	}

	// Something to undo: the script runs.
	ipv6Fixture(t)
	run := os.Getenv("NANOKVM_IPV6_RUN")
	if err := os.WriteFile(filepath.Join(run, "ipv6-static.eth0"), []byte("2001:db8::10/64 2001:db8::1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if done, err := applyIPv6Off(); done || err != nil {
		t.Fatalf("static address left: %v %v", done, err)
	}
	t.Setenv("NANOKVM_DHCPV6_RUN", run)
	if err := os.Remove(filepath.Join(run, "ipv6-static.eth0")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "udhcpc6.wlan0.pid"), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if done, err := applyIPv6Off(); done || err != nil {
		t.Fatalf("DHCPv6 client left: %v %v", done, err)
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
		if err != nil || policy["eth0"] != dhcpv6If(both) || policy["wlan0"] != dhcpv6If(both) {
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
	policy := ipv6Policy{"eth0": dhcpv6If(true)}
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
	if !status.Enabled || string(encoded) != `[{"name":"eth0","enabled":true,"mode":"dhcpv6","address":"","gateway":"","active":true,"present":true},{"name":"wlan0","enabled":false,"mode":"","address":"","gateway":"","active":false,"present":true}]` {
		t.Fatalf("status %s, enabled %t", encoded, status.Enabled)
	}
}

func TestIPv6RejectsCorruptConfigAndReportsUnsupportedKernel(t *testing.T) {
	ipv6Fixture(t)
	if err := writeIPv6Policy(ipv6Policy{"eth0": dhcpv6If(true)}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"yes", "eth0=2", "usb0=1", "eth0", "eth0=dhcpv6", "eth0=static", "eth0=static eth0.address=fe80::/64x", "eth0.mtu=1280", "wlan0.gateway=10.0.0.1"} {
		if err := os.WriteFile(ipv6ConfigFile, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readIPv6Policy(); err == nil {
			t.Fatalf("corrupt config %q accepted", content)
		}
	}
	ipv6SysctlRoot = filepath.Join(t.TempDir(), "no-ipv6")
	if err := applyIPv6(ipv6Policy{"wlan0": dhcpv6If(true)}); err == nil {
		t.Fatal("enabled unsupported IPv6")
	}
	if err := applyIPv6(ipv6Policy{}); err != nil {
		t.Fatal(err)
	}
}

func TestIPv6StaticAddressAndGateway(t *testing.T) {
	ipv6Fixture(t)
	for _, name := range []string{"eth0", "wlan0"} {
		for _, file := range []string{"autoconf", "keep_addr_on_down"} {
			value := "1\n"
			if file == "keep_addr_on_down" {
				value = "0\n"
			}
			if err := os.WriteFile(filepath.Join(ipv6SysctlRoot, name, file), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ipLog := os.Getenv("NANOKVM_IP") + ".log"
	clientLog := os.Getenv("NANOKVM_DHCPV6") + ".log"
	setIPv6Request(t, `{"enabled":true,"interface":"wlan0","mode":"static","address":"2001:db8::10/64","gateway":"2001:db8::1"}`)
	if sysctlFile(t, "wlan0", "autoconf") != "0" || sysctlFile(t, "wlan0", "keep_addr_on_down") != "1" {
		t.Fatal("static Wi-Fi keeps SLAAC or loses its address when down")
	}
	if calls := takeCalls(t, ipLog); calls != "-6 addr replace 2001:db8::10/64 dev wlan0\n-6 route replace default via 2001:db8::1 dev wlan0 metric 300" {
		t.Fatalf("ip calls %q", calls)
	}
	if calls := takeCalls(t, clientLog); calls != "stop eth0\nstop wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	// Another address without a gateway: the old ones are taken off.
	setIPv6Request(t, `{"enabled":true,"interface":"wlan0","mode":"static","address":"2001:db8::20/64","gateway":""}`)
	if calls := takeCalls(t, ipLog); calls != "-6 route del default via 2001:db8::1 dev wlan0\n-6 addr del 2001:db8::10/64 dev wlan0\n-6 addr replace 2001:db8::20/64 dev wlan0" {
		t.Fatalf("ip calls %q", calls)
	}
	if calls := takeCalls(t, clientLog); calls != "stop eth0\nstop wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	// Back to DHCPv6: the static address goes, SLAAC and the client return,
	// and IPv6 restarts on the interface for new router advertisements.
	setIPv6Request(t, `{"enabled":true,"interface":"wlan0","mode":"dhcpv6"}`)
	if calls := takeCalls(t, ipLog); calls != "-6 addr del 2001:db8::20/64 dev wlan0" {
		t.Fatalf("ip calls %q", calls)
	}
	if sysctlFile(t, "wlan0", "autoconf") != "1" || sysctlFile(t, "wlan0", "keep_addr_on_down") != "0" || sysctlValue(t, "wlan0") != "0" {
		t.Fatal("DHCPv6 mode not restored")
	}
	if calls := takeCalls(t, clientLog); calls != "stop eth0\nstart wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	// The static address is kept for the next time static is chosen.
	policy, _ := readIPv6Policy()
	if policy["wlan0"] != (ipv6Setting{Mode: ipv6DHCPv6, Address: "2001:db8::20/64"}) {
		t.Fatalf("policy %+v", policy["wlan0"])
	}
	for _, body := range []string{
		`{"enabled":true,"interface":"eth0","mode":"static"}`,
		`{"enabled":true,"interface":"eth0","mode":"static","address":"192.0.2.1/24"}`,
		`{"enabled":true,"interface":"eth0","mode":"static","address":"2001:db8::1"}`,
		`{"enabled":true,"interface":"eth0","mode":"static","address":"2001:db8::1/64","gateway":"10.0.0.1"}`,
		`{"enabled":true,"interface":"eth0","mode":"static","address":"2001:db8::1/64 dev lo"}`,
		`{"enabled":true,"mode":"static","address":"2001:db8::1/64"}`,
		`{"enabled":true,"interface":"eth0","mode":"slaac"}`,
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest("POST", "/api/network/ipv6", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		(&Service{}).SetIPv6(ctx)
		if !strings.Contains(response.Body.String(), `"code":-1`) {
			t.Fatalf("%s accepted: %s", body, response.Body.String())
		}
	}
}

// The static address and gateway are saved and passed to ip(8) as netip
// prints them, whatever the spelling of the request or the file.
func TestIPv6StaticValuesAreCanonical(t *testing.T) {
	ipv6Fixture(t)
	setIPv6Request(t, `{"enabled":true,"interface":"eth0","mode":"static","address":" 2001:DB8:0:0::0010/64 ","gateway":"2001:0db8::0001"}`)
	policy, err := readIPv6Policy()
	if err != nil || policy["eth0"] != (ipv6Setting{Mode: ipv6Static, Address: "2001:db8::10/64", Gateway: "2001:db8::1"}) {
		t.Fatalf("policy %+v, %v", policy["eth0"], err)
	}
	if calls := takeCalls(t, os.Getenv("NANOKVM_IP")+".log"); calls != "-6 addr replace 2001:db8::10/64 dev eth0\n-6 route replace default via 2001:db8::1 dev eth0 metric 200" {
		t.Fatalf("ip calls %q", calls)
	}
	// A file written before by hand or an older version reads canonical too.
	if err := os.WriteFile(ipv6ConfigFile, []byte("wlan0=static wlan0.address=2001:DB8::0020/64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	policy, err = readIPv6Policy()
	if err != nil || policy["wlan0"].Address != "2001:db8::20/64" {
		t.Fatalf("policy %+v, %v", policy["wlan0"], err)
	}
}

func TestIPv6AddressSource(t *testing.T) {
	previous := ipv6RunDir
	t.Cleanup(func() { ipv6RunDir = previous })
	ipv6RunDir = t.TempDir()
	// The DHCPv6 client records the address in busybox's expanded form.
	_ = os.WriteFile(filepath.Join(ipv6RunDir, "udhcpc6.wlan0.addr"), []byte("2a02:014c:bd40:0300:0000:0000:0000:01ce/128\n"), 0600)
	_ = os.WriteFile(filepath.Join(ipv6RunDir, "ipv6-static.eth0"), []byte("2001:db8::10/64 2001:db8::1\n"), 0600)
	for _, c := range []struct{ iface, address, want string }{
		{"wlan0", "2a02:14c:bd40:300::1ce", "dhcpv6"},
		{"wlan0", "2a02:14c:bd40:300:aab5:8eff:fe22:67b8", "slaac"},
		{"wlan0", "fe80::aab5:8eff:fe22:67b8", ""},
		{"eth0", "2001:db8::10", "static"},
		{"usb0", "2001:db8::99", ""},
	} {
		if got := ipv6Source(c.iface, netip.MustParseAddr(c.address)); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.iface, c.address, got, c.want)
		}
	}
}

func sysctlFile(t *testing.T, name, file string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(ipv6SysctlRoot, name, file))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(got))
}

func takeCalls(t *testing.T, log string) string {
	t.Helper()
	data, _ := os.ReadFile(log)
	_ = os.Remove(log)
	return strings.TrimSpace(string(data))
}

// Ethernet or Wi-Fi with IPv6 runs its DHCPv6 client next to SLAAC.
func TestIPv6RunsTheDHCPv6ClientWhileOn(t *testing.T) {
	ipv6Fixture(t)
	log := os.Getenv("NANOKVM_DHCPV6") + ".log"
	if err := applyIPv6(ipv6Policy{"wlan0": dhcpv6If(true)}); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(t, log); calls != "stop eth0\nstart wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	if err := applyIPv6(ipv6Policy{"eth0": dhcpv6If(true)}); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(t, log); calls != "start eth0\nstop wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	if err := writeIPv6Policy(ipv6Policy{"eth0": dhcpv6If(true)}); err != nil {
		t.Fatal(err)
	}
	// udev adds Wi-Fi, which is off: only its client is told.
	runIPv6Script(t, "interface", "wlan0")
	if calls := takeCalls(t, log); calls != "stop wlan0" {
		t.Fatalf("client calls %q", calls)
	}
	// An interface that does not exist yet gets no client.
	if err := os.RemoveAll(filepath.Join(ipv6SysctlRoot, "wlan0")); err != nil {
		t.Fatal(err)
	}
	if err := applyIPv6(ipv6Policy{"eth0": dhcpv6If(true), "wlan0": dhcpv6If(true)}); err != nil {
		t.Fatal(err)
	}
	if calls := takeCalls(t, log); calls != "start eth0" {
		t.Fatalf("client calls %q", calls)
	}
}

// udhcpc6 removes its PID on SIGTERM, but without -R leaves the lease and
// DNS behind. IPv6-off initialization must still run the cleanup script.
func TestIPv6OffCleansLeaseWithoutClientPID(t *testing.T) {
	for _, name := range ipv6Interfaces {
		t.Run(name, func(t *testing.T) {
			ipv6Fixture(t)
			run := os.Getenv("NANOKVM_DHCPV6_RUN")
			record := filepath.Join(run, "udhcpc6."+name+".addr")
			if err := os.WriteFile(record, []byte("2001:db8::10/128\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if done, err := applyIPv6Off(); done || err != nil {
				t.Fatalf("lease left without PID: %v %v", done, err)
			}
			bin := t.TempDir()
			stubCommand(t, bin, "ip")
			resolver := stubCommand(t, bin, "resolvconf")
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			helper, err := os.ReadFile("scripts/nanokvm-dhcpv6")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(bin, "dhcpv6")
			if err := os.WriteFile(path, helper, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("NANOKVM_DHCPV6", path)
			if err := applyIPv6(ipv6Policy{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("lease record remains: %v", err)
			}
			data, err := os.ReadFile(resolver + ".log")
			if err != nil || !strings.Contains(string(data), "-d nkos.dhcpv6."+name+" -f") {
				t.Fatalf("DNS cleanup: %q %v", data, err)
			}
			data, err = os.ReadFile(filepath.Join(bin, "ip.log"))
			if err != nil || !strings.Contains(string(data), "-6 addr del 2001:db8::10/128 dev "+name) {
				t.Fatalf("address cleanup: %q %v", data, err)
			}
			assertIPv6(t, false, false)
			if done, err := applyIPv6Off(); !done || err != nil {
				t.Fatalf("cleaned state: %v %v", done, err)
			}
		})
	}
}
