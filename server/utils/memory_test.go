package utils

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// fakeRuntime stands in for debug.SetMemoryLimit, including its -1 query.
type fakeRuntime struct{ limit int64 }

func (r *fakeRuntime) apply(n int64) int64 {
	previous := r.limit
	if n >= 0 {
		r.limit = n
	}
	return previous
}

func newDefaultStore(t *testing.T, meminfo string) (*goMemoryLimitStore, *fakeRuntime) {
	t.Helper()
	dir := t.TempDir()
	runtime := &fakeRuntime{limit: math.MaxInt64}
	store := &goMemoryLimitStore{path: filepath.Join(dir, "limit"), apply: runtime.apply, memInfo: filepath.Join(dir, "meminfo")}
	if meminfo != "" {
		if err := os.WriteFile(store.memInfo, []byte(meminfo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return store, runtime
}

const deviceMemInfo = "MemTotal:         106924 kB\nMemFree:           12704 kB\nMemAvailable:      49240 kB\n"

func TestGoMemoryLimitDefaultsToQuarterOfMemTotal(t *testing.T) {
	store, runtime := newDefaultStore(t, deviceMemInfo)
	store.init()
	if want := int64(106924 * 1024 / 4); runtime.limit != want {
		t.Fatalf("default limit=%d want %d", runtime.limit, want)
	}
	// Turning a saved limit off returns to the default, not to no limit.
	if err := store.set(80); err != nil || runtime.limit != 80*1024*1024 {
		t.Fatalf("set: %d %v", runtime.limit, err)
	}
	if err := store.remove(); err != nil || runtime.limit != 106924*1024/4 {
		t.Fatalf("remove: %d %v", runtime.limit, err)
	}
}

// With the shared video region MemTotal includes 144 MiB that 2160p capture
// takes back but for 40 MiB: the default follows what Linux keeps then.
func TestGoMemoryLimitDefaultLeavesOutTheVideoRegion(t *testing.T) {
	store, runtime := newDefaultStore(t, "MemTotal:         240992 kB\nMemFree:           98424 kB\nCmaTotal:         147456 kB\n")
	store.init()
	if want := int64((240992 - 147456 + 147456 - 106496) * 1024 / 4); runtime.limit != want || toMiB(want) != 32 {
		t.Fatalf("default limit=%d want %d (%d MiB)", runtime.limit, want, toMiB(want))
	}
}

func TestGoMemoryLimitRejectsOverflowAndCorruptSetting(t *testing.T) {
	calls := 0
	store := goMemoryLimitStore{path: filepath.Join(t.TempDir(), "limit"),
		apply: func(int64) int64 { calls++; return 0 }}
	for _, value := range []int64{-1, math.MaxInt64/(1024*1024) + 1, math.MaxInt64} {
		if err := store.set(value); err == nil {
			t.Fatalf("accepted %d", value)
		}
	}
	if calls != 0 {
		t.Fatal("invalid input changed runtime")
	}
	for _, value := range []string{"", "bad", "75 MiB", "-1", "9223372036854775807"} {
		if err := os.WriteFile(store.path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := store.read(); err == nil {
			t.Fatalf("accepted saved %q", value)
		}
	}
}
