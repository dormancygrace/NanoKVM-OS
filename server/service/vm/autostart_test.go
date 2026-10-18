package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func temporaryAutostart(t *testing.T) string {
	t.Helper()
	oldList, oldRun := autostartList, autostartRunDir
	dir := t.TempDir()
	autostartList = filepath.Join(dir, "autostart.list")
	autostartRunDir = filepath.Join(dir, "run")
	t.Cleanup(func() { autostartList, autostartRunDir = oldList, oldRun })
	return filepath.Join(dir, "scripts")
}

func TestAutostartListKeepsExistingScriptsSorted(t *testing.T) {
	root := temporaryAutostart(t)
	writeTestScript(t, filepath.Join(root, "b.sh"), "#!/bin/sh\n")
	writeTestScript(t, filepath.Join(root, "dir", "a.py"), "pass\n")
	if err := setAutostart(root, "missing.sh", true); err == nil {
		t.Fatal("a missing script was marked")
	}
	if err := setAutostart(root, "../x.sh", false); err == nil {
		t.Fatal("an invalid name was accepted")
	}
	for _, name := range []string{"b.sh", "dir/a.py", "b.sh"} {
		if err := setAutostart(root, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if names, _ := readAutostart(); !reflect.DeepEqual(names, []string{"b.sh", "dir/a.py"}) {
		t.Fatalf("names %v", names)
	}
	for _, name := range []string{"b.sh", "dir/a.py"} {
		if err := setAutostart(root, name, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(autostartList); !os.IsNotExist(err) {
		t.Fatalf("an empty list stays as a file: %v", err)
	}
}

func waitForStatus(t *testing.T, name, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(autostartBase(name) + ".status")
		got := strings.TrimSpace(string(data))
		if strings.HasPrefix(got, want) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunAutostartOncePerBootWithLogAndStatus(t *testing.T) {
	root := temporaryAutostart(t)
	counter := filepath.Join(t.TempDir(), "count")
	writeTestScript(t, filepath.Join(root, "ok.sh"), "#!/bin/sh\necho started >> "+counter+"\npwd\nexit 3\n")
	writeTestScript(t, filepath.Join(root, "loud.sh"), "#!/bin/sh\nhead -c 200000 /dev/zero | tr '\\0' x\necho done\n")
	writeTestScript(t, filepath.Join(root, "gone.sh"), "#!/bin/sh\n")
	for _, name := range []string{"ok.sh", "loud.sh", "gone.sh"} {
		if err := setAutostart(root, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "gone.sh")); err != nil {
		t.Fatal(err)
	}

	RunAutostart(root)
	RunAutostart(root) // a server restart in the same boot
	if got := waitForStatus(t, "ok.sh", "exited"); got != "exited 3" {
		t.Fatalf("ok.sh status %q", got)
	}
	if got := waitForStatus(t, "loud.sh", "exited"); got != "exited 0" {
		t.Fatalf("loud.sh status %q", got)
	}
	if got := waitForStatus(t, "gone.sh", "error"); !strings.HasPrefix(got, "error:") {
		t.Fatalf("gone.sh status %q", got)
	}
	if data, _ := os.ReadFile(counter); string(data) != "started\n" {
		t.Fatalf("ran %q times", string(data))
	}
	if data, _ := os.ReadFile(autostartBase("ok.sh") + ".log"); strings.TrimSpace(string(data)) != root {
		t.Fatalf("ok.sh log %q, want its directory", string(data))
	}
	if info, err := os.Stat(autostartBase("loud.sh") + ".log"); err != nil || info.Size() != autostartLogLimit {
		t.Fatalf("loud.sh log size %v, %v", info, err)
	}
	status := autostartStatus([]string{"ok.sh", "loud.sh", "never.sh"})
	if status["ok.sh"] != "exited 3" || status["loud.sh"] != "exited 0" || status["never.sh"] != "" {
		t.Fatalf("status %v", status)
	}
}

func TestAutostartConcurrentMarksAreNotLost(t *testing.T) {
	root := temporaryAutostart(t)
	const count = 32
	for i := range count {
		writeTestScript(t, filepath.Join(root, fmt.Sprintf("%d.sh", i)), "#!/bin/sh\n")
	}
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- setAutostart(root, fmt.Sprintf("%d.sh", i), true) }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	names, err := readAutostart()
	if err != nil || len(names) != count {
		t.Fatalf("lost marks: %d/%d: %v", len(names), count, err)
	}
}

func TestAutostartPreservesNamesAndSeparateLogs(t *testing.T) {
	root := temporaryAutostart(t)
	names := []string{"dir/a.sh", "dir__a.sh"}
	for _, name := range names {
		writeTestScript(t, filepath.Join(root, name), "#!/bin/sh\necho '"+name+"'\n")
		if err := setAutostart(root, name, true); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := readAutostart()
	if err != nil || !reflect.DeepEqual(stored, names) {
		t.Fatalf("names changed: %q, %v", stored, err)
	}
	RunAutostart(root)
	for _, name := range names {
		if got := waitForStatus(t, name, "exited"); got != "exited 0" {
			t.Fatalf("%s: %s", name, got)
		}
		data, err := os.ReadFile(autostartBase(name) + ".log")
		if err != nil || string(data) != name+"\n" {
			t.Errorf("%s log = %q, %v", name, data, err)
		}
	}
}

func TestAutostartPreservesLeadingSpaces(t *testing.T) {
	root := temporaryAutostart(t)
	name := " leading.sh"
	writeTestScript(t, filepath.Join(root, name), "#!/bin/sh\n")
	if err := setAutostart(root, name, true); err != nil {
		t.Fatal(err)
	}
	names, err := readAutostart()
	if err != nil || !reflect.DeepEqual(names, []string{name}) {
		t.Fatalf("name changed: %q, %v", names, err)
	}
	if err := setAutostart(root, name, false); err != nil {
		t.Fatal(err)
	}
	names, err = readAutostart()
	if err != nil || len(names) != 0 {
		t.Fatalf("could not unmark: %q, %v", names, err)
	}
}
