package vm

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeTestScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestListScriptsPreservesRelativePathsAndSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestScript(t, filepath.Join(root, "a", "job.sh"), "#!/bin/sh\n")
	writeTestScript(t, filepath.Join(root, "b", "job.sh"), "#!/bin/sh\n")
	writeTestScript(t, filepath.Join(root, "space name.py"), "pass\n")
	writeTestScript(t, filepath.Join(root, "ignored.txt"), "ignored\n")

	outside := filepath.Join(t.TempDir(), "outside.sh")
	writeTestScript(t, outside, "#!/bin/sh\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	got, err := listScripts(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/job.sh", "b/job.sh", "space name.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scripts = %#v, want %#v", got, want)
	}
}

func TestListScriptsReturnsEmptyWhenDirectoryDoesNotExist(t *testing.T) {
	files, err := listScripts(filepath.Join(t.TempDir(), "not-created"))
	if err != nil {
		t.Fatal(err)
	}
	if files == nil || len(files) != 0 {
		t.Fatalf("files = %#v", files)
	}
}

func TestScriptPathRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "nested", "job.sh")
	writeTestScript(t, valid, "#!/bin/sh\n")
	got, err := scriptPath(root, "nested/job.sh")
	if err != nil || got != valid {
		t.Fatalf("valid path = %q, %v", got, err)
	}

	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.sh")
	writeTestScript(t, outside, "#!/bin/sh\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	invalid := []string{
		"", ".", "..", "../outside.sh", "nested/../../outside.sh",
		"nested/../nested/job.sh", "./nested/job.sh", "/etc/passwd.sh",
		`nested\job.sh`, "missing.sh", "linked.sh", "linked-dir/outside.sh",
		"directory.sh", "nested/job.txt", "nested//job.sh", "nested/job.sh\n",
	}
	for _, name := range invalid {
		if path, err := scriptPath(root, name); err == nil {
			t.Errorf("accepted %q as %q", name, path)
		}
	}

	rootLink := filepath.Join(t.TempDir(), "scripts")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := scriptPath(rootLink, "nested/job.sh"); !errors.Is(err, errInvalidScript) {
		t.Fatalf("symlink root error = %v", err)
	}
}

func TestScriptCommandDoesNotInterpretShellMetacharacters(t *testing.T) {
	root := t.TempDir()
	name := "space $(touch PWNED); value.sh"
	path := filepath.Join(root, name)
	writeTestScript(t, path, "#!/bin/sh\nprintf 'safe output'\n")

	resolved, err := scriptPath(root, name)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := scriptCommand(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != resolved {
		t.Fatalf("shell script argv = %#v", cmd.Args)
	}
	working := t.TempDir()
	cmd.Dir = working
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "safe output" {
		t.Fatalf("output = %q, error = %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(working, "PWNED")); !os.IsNotExist(err) {
		t.Fatal("shell metacharacters were interpreted")
	}

	interpreter := filepath.Join(root, "python3")
	writeTestScript(t, interpreter, "#!/bin/sh\n")
	originalInterpreter := pythonInterpreter
	pythonInterpreter = interpreter
	t.Cleanup(func() { pythonInterpreter = originalInterpreter })

	pythonPath := filepath.Join(root, "name; touch PWNED.py")
	python, err := scriptCommand(pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(python.Args) != 2 || python.Args[0] != interpreter || python.Args[1] != pythonPath {
		t.Fatalf("python argv = %#v", python.Args)
	}
}

func TestScriptCommandReportsMissingPythonAddon(t *testing.T) {
	originalInterpreter := pythonInterpreter
	pythonInterpreter = filepath.Join(t.TempDir(), "missing-python3")
	t.Cleanup(func() { pythonInterpreter = originalInterpreter })

	if _, err := scriptCommand(filepath.Join(t.TempDir(), "job.py")); !errors.Is(err, errPythonRuntimeMissing) {
		t.Fatalf("missing Python error = %v", err)
	}
}

func TestBackgroundStartReportsFailureAndRunsValidScript(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "finished")
	path := filepath.Join(root, "background.sh")
	writeTestScript(t, path, "#!/bin/sh\nprintf done > '"+marker+"'\n")
	if output, err := executeScript(path, "background"); err != nil || output != nil {
		t.Fatalf("background start = %q, %v", output, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(marker); err == nil {
			if string(data) != "done" {
				t.Fatalf("marker = %q", data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background script did not run")
		}
		time.Sleep(time.Millisecond)
	}

	notExecutable := filepath.Join(root, "not-executable.sh")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeScript(notExecutable, "background"); err == nil {
		t.Fatal("background start failure was hidden")
	}
	if _, err := executeScript(path, "unexpected"); !errors.Is(err, errInvalidScript) {
		t.Fatalf("invalid type error = %v", err)
	}
}

func TestSaveScriptIsFlatAtomicAndDoesNotFollowTargetSymlink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scripts")
	target, err := saveScript(root, "uploaded.sh", strings.NewReader("#!/bin/sh\necho ok\n"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}

	for _, name := range []string{"../escape.sh", "nested/job.sh", "/tmp/escape.sh", `C:\escape.sh`, "job.txt", "bad\nname.sh"} {
		if _, err := saveScript(root, name, bytes.NewBufferString("bad")); !errors.Is(err, errInvalidScript) {
			t.Errorf("save %q error = %v", name, err)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside.sh")
	writeTestScript(t, outside, "outside\n")
	alias := filepath.Join(root, "alias.sh")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := saveScript(root, "alias.sh", strings.NewReader("replacement\n")); err != nil {
		t.Fatal(err)
	}
	aliasInfo, err := os.Lstat(alias)
	if err != nil || !aliasInfo.Mode().IsRegular() {
		t.Fatalf("upload did not replace symlink safely: %v, %v", aliasInfo, err)
	}
	outsideData, err := os.ReadFile(outside)
	if err != nil || string(outsideData) != "outside\n" {
		t.Fatalf("outside target changed: %q, %v", outsideData, err)
	}
}

func TestRemoveScriptRejectsEscapesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "nested", "job.sh")
	writeTestScript(t, valid, "#!/bin/sh\n")
	outside := filepath.Join(t.TempDir(), "outside.sh")
	writeTestScript(t, outside, "outside\n")
	link := filepath.Join(root, "linked.sh")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../outside.sh", "linked.sh"} {
		if err := removeScript(root, name); err == nil {
			t.Fatalf("removed through %q", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file changed: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink was removed: %v", err)
	}
	if err := removeScript(root, "nested/job.sh"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(valid); !os.IsNotExist(err) {
		t.Fatalf("valid script still exists: %v", err)
	}
}
