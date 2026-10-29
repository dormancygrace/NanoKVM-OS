package network

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withHookPaths(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	oldDirs, oldLegacy, oldConf := udhcpcHookDirs, legacyHookDir, udhcpcConfFile
	udhcpcHookDirs = []string{filepath.Join(root, "post-bound"), filepath.Join(root, "post-renew")}
	legacyHookDir = filepath.Join(root, "default.script.d")
	udhcpcConfFile = filepath.Join(root, "udhcpc.conf")
	t.Cleanup(func() { udhcpcHookDirs, legacyHookDir, udhcpcConfFile = oldDirs, oldLegacy, oldConf })
	return root
}

func TestDHCPHooksAreInstalledWhereAlpineRunsThem(t *testing.T) {
	withHookPaths(t)
	if err := os.MkdirAll(legacyHookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyHookDir, udhcpcDNSHookName)
	if err := os.WriteFile(legacy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installUDHCPCHook(udhcpcDNSHookName, udhcpcDNSHook); err != nil {
		t.Fatal(err)
	}
	for _, dir := range udhcpcHookDirs {
		info, err := os.Stat(filepath.Join(dir, udhcpcDNSHookName))
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("hook in %s missing or not executable: %v", dir, err)
		}
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("the Buildroot hook path was not removed: %v", err)
	}
}

func TestUDHCPCLeavesResolvConfToOpenresolvOnce(t *testing.T) {
	withHookPaths(t)
	if err := os.WriteFile(udhcpcConfFile, []byte("# Prevent overwriting\n#RESOLV_CONF=\"no\""), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ensureUDHCPCLeavesResolvConf(); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(udhcpcConfFile)
	want := "# Prevent overwriting\n#RESOLV_CONF=\"no\"\n" + udhcpcResolvLine + "\n"
	if string(data) != want {
		t.Fatalf("udhcpc.conf =\n%s\nwant\n%s", data, want)
	}
}

func TestBaseResolverRecoversFromSignatureMismatch(t *testing.T) {
	old := resolvconfRunner
	t.Cleanup(func() { resolvconfRunner = old })
	var calls []string
	mismatch := true
	resolvconfRunner = func(tool, input string, args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "-a" && mismatch {
			mismatch = false
			return errors.New("exit status 1: resolvconf: signature mismatch: /etc/resolv.conf")
		}
		return nil
	}
	if err := publishBaseResolver("resolvconf", "nameserver 192.0.2.1\n"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"-a nkos.base", "-u", "-a nkos.base"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls %v, want %v", calls, want)
	}

	calls = nil
	resolvconfRunner = func(string, string, ...string) error {
		calls = append(calls, "-a")
		return errors.New("exit status 1: permission denied")
	}
	if err := publishBaseResolver("resolvconf", "nameserver 192.0.2.1\n"); err == nil || len(calls) != 1 {
		t.Fatalf("other failures must not take the file over: err=%v calls=%v", err, calls)
	}
}

// Alpine runs the hooks from /etc/udhcpc/post-<event>/ without arguments; they
// must act on bound and renew from there, and not on other events.
func TestDHCPHooksActWithoutArguments(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls")
	for _, tool := range []string{"resolvconf", "ip"} {
		fake := "#!/bin/sh\nprintf '" + tool + " %s\\n' \"$*\" >> \"$CALLS\"\n[ " + tool + " = resolvconf ] && cat >> \"$CALLS\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	preference := filepath.Join(root, "gateway.preferred")
	if err := os.WriteFile(preference, []byte("wifi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dns := strings.NewReplacer(
		"/etc/resolv.conf.dhcp", filepath.Join(root, "resolv.dhcp"),
		"/etc/resolv.conf", filepath.Join(root, "resolv.conf"),
		"/etc/kvm/network/dns.mode", filepath.Join(root, "dns.mode"),
		"/boot/resolv.conf", filepath.Join(root, "boot.resolv"),
	).Replace(udhcpcDNSHook)
	gateway := strings.NewReplacer(
		gatewayPreferenceFile, preference,
		"/etc/udhcpc/udhcpc.conf", filepath.Join(root, "udhcpc.conf"),
	).Replace(udhcpcGatewayHook)
	env := append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "CALLS="+calls,
		"interface=wlan0", "dns=192.0.2.53", "search=example.test", "router=192.0.2.1")

	run := func(event string, args ...string) string {
		t.Helper()
		dir := filepath.Join(root, event)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(calls)
		for name, script := range map[string]string{udhcpcDNSHookName: dns, udhcpcGatewayHookName: gateway} {
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(path, args...)
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s/%s: %v\n%s", event, name, err, out)
			}
		}
		data, _ := os.ReadFile(calls)
		return string(data)
	}

	for _, event := range []string{"post-bound", "post-renew"} {
		got := run(event)
		for _, want := range []string{
			"resolvconf -a nkos.base\nsearch example.test\nnameserver 192.0.2.53\n",
			"ip -4 route replace default via 192.0.2.1 dev wlan0 proto dhcp metric 10\n",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s: missing %q in\n%s", event, want, got)
			}
		}
	}
	if got := run("post-deconfig"); got != "" {
		t.Fatalf("post-deconfig must do nothing, got\n%s", got)
	}
	// An explicit event argument still wins (the Buildroot calling convention).
	if got := run("post-bound", "deconfig"); got != "" {
		t.Fatalf("an explicit deconfig must do nothing, got\n%s", got)
	}
}

func TestManualDNSWithdrawsOnlyDHCPv6Resolvers(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "resolvconf"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	old := resolvconfRunner
	t.Cleanup(func() { resolvconfRunner = old })
	entries := map[string]bool{"nkos.base": true, "nkos.dhcpv6.eth0": true, "nkos.dhcpv6.wlan0": true, "tun0": true}
	resolvconfRunner = func(tool, input string, args ...string) error {
		if len(args) != 3 || args[0] != "-d" || args[2] != "-f" {
			t.Fatalf("unexpected resolver command: %v", args)
		}
		delete(entries, args[1])
		return nil
	}
	if err := clearDHCPv6Resolvers(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entries, map[string]bool{"nkos.base": true, "tun0": true}) {
		t.Fatalf("manual DNS kept automatic servers or removed VPN DNS: %v", entries)
	}
	resolvconfRunner = func(string, string, ...string) error { return errors.New("resolver failed") }
	if err := clearDHCPv6Resolvers(); err == nil {
		t.Fatal("resolver removal failure was hidden")
	}
}

func TestAdoptResolverBeforeDHCP(t *testing.T) {
	for _, base := range []string{"", "\n", "# Waiting for DHCP\n"} {
		t.Run(strings.ReplaceAll(base, "\n", "_"), func(t *testing.T) {
			old := resolvconfRunner
			t.Cleanup(func() { resolvconfRunner = old })
			// DHCP can submit its key before startup adopts the empty file.
			// A VPN entry must survive the handover too.
			entries := map[string]string{
				"nkos.base": "nameserver 192.0.2.53\n",
				"wg0":       "nameserver 198.51.100.53\n",
			}
			owned := false
			resolvconfRunner = func(tool, input string, args ...string) error {
				switch strings.Join(args, " ") {
				case "-u":
					owned = true
				case "-a nkos.base":
					if !owned {
						return errors.New("signature mismatch")
					}
					entries["nkos.base"] = input
				default:
					t.Fatalf("unexpected command: %s %v", tool, args)
				}
				return nil
			}
			if err := adoptBaseResolver("resolvconf", base); err != nil {
				t.Fatal(err)
			}
			if !owned {
				t.Fatal("startup left the empty resolver unmanaged")
			}
			if entries["nkos.base"] != "nameserver 192.0.2.53\n" ||
				entries["wg0"] != "nameserver 198.51.100.53\n" {
				t.Fatalf("adoption replaced existing DHCP/VPN DNS: %v", entries)
			}
			// The DHCP hook uses -a directly, without a takeover retry.
			if err := resolvconfRunner("resolvconf", "nameserver 203.0.113.53\n", "-a", "nkos.base"); err != nil {
				t.Fatalf("a later DHCP lease cannot publish DNS: %v", err)
			}
			if entries["nkos.base"] != "nameserver 203.0.113.53\n" ||
				entries["wg0"] != "nameserver 198.51.100.53\n" {
				t.Fatalf("DHCP renewal lost DNS or changed VPN DNS: %v", entries)
			}
		})
	}
}

func TestAdoptResolverPreservesBaseAndReportsFailures(t *testing.T) {
	old := resolvconfRunner
	t.Cleanup(func() { resolvconfRunner = old })
	base := "search example.test\nnameserver 192.0.2.53\n"
	for _, failAt := range []string{"", "-u", "-a nkos.base"} {
		t.Run(failAt, func(t *testing.T) {
			var calls []string
			failure := errors.New("resolver unavailable")
			resolvconfRunner = func(tool, input string, args ...string) error {
				command := strings.Join(args, " ")
				calls = append(calls, command)
				if command == failAt {
					return failure
				}
				if command == "-a nkos.base" && input != base {
					t.Fatalf("lost existing resolver settings: %q", input)
				}
				return nil
			}
			err := adoptBaseResolver("resolvconf", base)
			if (failAt == "" && err != nil) || (failAt != "" && !errors.Is(err, failure)) {
				t.Fatalf("failure %q: got %v", failAt, err)
			}
			want := []string{"-u", "-a nkos.base"}
			if failAt == "-u" {
				want = want[:1]
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls %v, want %v", calls, want)
			}
		})
	}
}
