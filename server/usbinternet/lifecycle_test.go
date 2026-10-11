package usbinternet

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func activeTestRuntime(t *testing.T) Runtime {
	t.Helper()
	d := t.TempDir()
	r := Runtime{Boot: filepath.Join(d, "boot"), Run: filepath.Join(d, "run"), Gadget: filepath.Join(d, "gadget"), Net: filepath.Join(d, "net"), Proc: filepath.Join(d, "proc")}
	for _, p := range []string{r.Boot, r.Run, filepath.Join(r.Gadget, "configs/c.1"), filepath.Join(r.Net, "usb0")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.Gadget, "UDC"), []byte("controller\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../functions/ncm.usb0", filepath.Join(r.Gadget, "configs/c.1/ncm.usb0")); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWatchLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(Runtime) error
		watch  bool
		state  string
	}{
		{"local NCM", func(r Runtime) error { return nil }, true, "unavailable"},
		{"unbound", func(r Runtime) error { return os.WriteFile(filepath.Join(r.Gadget, "UDC"), nil, 0600) }, false, "disabled"},
		{"HID only", func(r Runtime) error { return os.Remove(filepath.Join(r.Gadget, "configs/c.1/ncm.usb0")) }, false, "disabled"},
		{"missing interface", func(r Runtime) error { return os.Remove(filepath.Join(r.Net, "usb0")) }, false, "disabled"},
		{"manual DHCP", func(r Runtime) error { return os.WriteFile(filepath.Join(r.Boot, "rndis.nodhcpd"), nil, 0600) }, false, "disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := activeTestRuntime(t)
			if err := tc.mutate(r); err != nil {
				t.Fatal(err)
			}
			if r.WatchNeeded() != tc.watch {
				t.Fatal("incorrect watcher lifecycle")
			}
			if s := r.ReadStatus(); s.State != tc.state {
				t.Fatal(s)
			}
		})
	}
}

func TestIdleStatusDoesNotExpireOrHideCleanupErrors(t *testing.T) {
	r := activeTestRuntime(t)
	if err := os.WriteFile(filepath.Join(r.Gadget, "UDC"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Boot, "usb.internet"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"waiting-ncm", "error"} {
		if err := r.save("status.json", Status{Enabled: true, State: state, UpdatedAt: time.Now().Unix() - 60}); err != nil {
			t.Fatal(err)
		}
		if s := r.ReadStatus(); s.State != state || !s.Enabled || s.IPv6 {
			t.Fatal(s)
		}
	}
	if err := os.Remove(filepath.Join(r.Run, "status.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Boot, "rndis.nodhcpd"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if s := r.ReadStatus(); s.State != "dhcp-disabled" {
		t.Fatal(s)
	}
}
