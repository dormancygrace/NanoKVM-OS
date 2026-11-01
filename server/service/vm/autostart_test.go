package vm

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// Script exit and pipe draining are independent: a short script can publish
// its exit status before head has copied the log. Wait for the bytes separately.
func waitForAutostartLog(t *testing.T, name, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(autostartBase(name) + ".log")
		if err == nil && string(data) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s log = %q, %v; want %q", name, data, err, want)
		}
		time.Sleep(20 * time.Millisecond)
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
		waitForAutostartLog(t, name, name+"\n")
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
