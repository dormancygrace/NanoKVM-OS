package main

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name, kind string, paths, trees []string) target {
	t.Helper()
	return target{Name: name, Kind: kind, Paths: paths, Trees: trees, Before: fingerprint(paths, trees)}
}
func replace(t *testing.T, p string) {
	t.Helper()
	if e := os.WriteFile(p+".new", []byte("updated"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(p+".new", p); e != nil {
		t.Fatal(e)
	}
}
func TestSharedLibraryOnlyRestartsConsumers(t *testing.T) {
	d := t.TempDir()
	lib := filepath.Join(d, "libtest.so")
	other := filepath.Join(d, "unrelated")
	os.WriteFile(lib, []byte("old"), 0600)
	os.WriteFile(other, []byte("old"), 0600)
	s := snapshot{Targets: []target{fixture(t, "first", "service", []string{lib}, nil), fixture(t, "second", "service", []string{lib}, nil), fixture(t, "unrelated", "service", []string{other}, nil)}}
	replace(t, lib)
	todo, deferred := plan(s, func(target) bool { return true })
	if len(todo) != 2 || len(deferred) != 0 || todo[0].Name != "first" || todo[1].Name != "second" {
		t.Fatalf("unexpected plan: %+v %v", todo, deferred)
	}
}
func TestUtilityDoesNotRestartAndStoppedServiceStaysStopped(t *testing.T) {
	d := t.TempDir()
	exe := filepath.Join(d, "daemon")
	os.WriteFile(exe, []byte("old"), 0600)
	s := snapshot{Targets: []target{fixture(t, "daemon", "service", []string{exe}, nil)}}
	os.WriteFile(filepath.Join(d, "nano"), []byte("new"), 0600)
	todo, _ := plan(s, func(target) bool { return true })
	if len(todo) != 0 {
		t.Fatal("unrelated utility restarts daemon")
	}
	replace(t, exe)
	todo, _ = plan(s, func(target) bool { return false })
	if len(todo) != 0 {
		t.Fatal("stopped service started")
	}
}
func TestWebOnlyUpdateDoesNotRestartButBackendDoes(t *testing.T) {
	d := t.TempDir()
	web := filepath.Join(d, "server/web")
	os.MkdirAll(web, 0755)
	os.MkdirAll(filepath.Join(d, "server/dl_lib"), 0755)
	binary := filepath.Join(d, "server/NanoKVM-Server")
	os.WriteFile(binary, []byte("old"), 0755)
	index := filepath.Join(web, "index.html")
	os.WriteFile(index, []byte("old"), 0644)
	paths, trees := appRuntimePaths(d)
	s := snapshot{Targets: []target{fixture(t, "nanokvm-app", "service", paths, trees)}}
	os.Remove(index)
	os.WriteFile(filepath.Join(web, "new.js"), []byte("new"), 0644)
	todo, _ := plan(s, func(target) bool { return true })
	if len(todo) != 0 {
		t.Fatal("web-only update restarts video server")
	}
	replace(t, binary)
	todo, _ = plan(s, func(target) bool { return true })
	if len(todo) != 1 {
		t.Fatal("backend update must restart app once")
	}
	s = snapshot{Targets: []target{fixture(t, "nanokvm-app", "service", paths, trees)}}
	os.WriteFile(filepath.Join(d, "server/dl_lib/new.so"), []byte("new library"), 0644)
	todo, _ = plan(s, func(target) bool { return true })
	if len(todo) != 1 {
		t.Fatal("native library update must restart app once")
	}
}
func TestKernelIsDeferredNotRestarted(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "boot.sd")
	os.WriteFile(p, []byte("old"), 0600)
	s := snapshot{Targets: []target{fixture(t, "kernel activation", "firmware", []string{p}, nil)}}
	replace(t, p)
	todo, deferred := plan(s, func(target) bool { return true })
	if len(todo) != 0 || len(deferred) != 1 {
		t.Fatalf("unexpected kernel plan: %+v %v", todo, deferred)
	}
}
func TestOpenVPNRestoresViaRunningAppOnlyOnce(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "openvpn")
	os.WriteFile(p, []byte("old"), 0600)
	s := snapshot{Targets: []target{fixture(t, "openvpn", "openvpn", []string{p}, nil), fixture(t, "nanokvm-app", "service", nil, nil)}}
	replace(t, p)
	todo, _ := plan(s, func(target) bool { return true })
	if len(todo) != 2 {
		t.Fatalf("expected VPN and app actions: %+v", todo)
	}
	todo, _ = plan(s, func(x target) bool { return x.Name != "nanokvm-app" })
	if len(todo) != 0 {
		t.Fatal("must not stop VPN without a running application to restore it")
	}
}
func TestFingerprintIgnoresReadAccess(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "lib.so")
	os.WriteFile(p, []byte("same"), 0600)
	x := fixture(t, "daemon", "service", []string{p}, nil)
	os.ReadFile(p)
	if changed(x) {
		t.Fatal("reading unchanged file created restart")
	}
}

func TestUsesOpenRCOrder(t *testing.T) {
	todo := []target{{Name: "app", Kind: "service"}, {Name: "network", Kind: "service"}, {Name: "policy", Kind: "service"}}
	orderTargets(todo, map[string]int{"network": 1, "policy": 2, "app": 3})
	if todo[0].Name != "network" || todo[1].Name != "policy" || todo[2].Name != "app" {
		t.Fatalf("did not follow OpenRC order: %+v", todo)
	}
}
