package toolcache

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutputIsKeptUntilTheProgramIsReplaced(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "wg")
	if err := os.WriteFile(tool, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	runs := 0
	c := New(func(path string, args ...string) (string, error) {
		runs++
		data, err := os.ReadFile(path)
		return string(data) + " " + args[0], err
	}, time.Minute)
	for range 3 {
		if out, err := c.Output(tool, "--version"); err != nil || out != "v1 --version" {
			t.Fatalf("%q, %v", out, err)
		}
	}
	if runs != 1 {
		t.Fatalf("ran %d times for one program", runs)
	}
	if out, _ := c.Output(tool, "version"); out != "v1 version" || runs != 2 {
		t.Fatalf("other arguments shared an entry: %q", out)
	}
	// An upgrade renames a new file into place.
	next := filepath.Join(dir, ".wg.new")
	_ = os.WriteFile(next, []byte("v2"), 0o755)
	if err := os.Rename(next, tool); err != nil {
		t.Fatal(err)
	}
	if out, _ := c.Output(tool, "--version"); out != "v2 --version" || runs != 3 {
		t.Fatalf("replaced program not run again: %q", out)
	}
}

func TestFailuresAreRetriedAndMissingFilesNotCached(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "netbird")
	_ = os.WriteFile(tool, nil, 0o755)
	runs, failure := 0, errors.New("timed out")
	now := time.Unix(1000, 0)
	c := New(func(string, ...string) (string, error) { runs++; return "partial", failure }, time.Minute)
	c.now = func() time.Time { return now }
	if out, err := c.Output(tool, "version"); err == nil || out != "partial" {
		t.Fatalf("%q, %v", out, err)
	}
	if _, err := c.Output(tool, "version"); err == nil || runs != 1 {
		t.Fatal("failure not kept for the retry period")
	}
	now = now.Add(time.Minute)
	failure = nil
	if _, err := c.Output(tool, "version"); err != nil || runs != 2 {
		t.Fatal("failure not retried")
	}
	missing := filepath.Join(t.TempDir(), "absent")
	_, _ = c.Output(missing, "version")
	_, _ = c.Output(missing, "version")
	if runs != 4 {
		t.Fatalf("missing program cached (%d runs)", runs)
	}
}
