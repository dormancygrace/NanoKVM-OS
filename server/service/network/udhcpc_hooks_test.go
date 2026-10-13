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
	gateway := strings.ReplaceAll(udhcpcGatewayHook, gatewayPreferenceFile, preference)
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
