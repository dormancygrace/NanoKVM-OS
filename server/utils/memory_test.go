package utils

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestGoMemoryLimitPersistsEffectiveValue(t *testing.T) {
	var applied int64
	store := goMemoryLimitStore{path: filepath.Join(t.TempDir(), "limit"),
		apply: func(n int64) int64 { applied = n; return 0 }}
	for _, input := range []int64{0, 25, 50, 75} {
		if err := store.set(input); err != nil {
			t.Fatal(err)
		}
		saved, err := store.read()
		if err != nil {
			t.Fatal(err)
		}
		if want := max(input, int64(50)); saved != want || applied != want*1024*1024 {
			t.Fatalf("input=%d saved=%d applied=%d", input, saved, applied)
		}
	}
	if err := store.remove(); err != nil {
		t.Fatal(err)
	}
	if err := store.remove(); err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
	if applied != math.MaxInt64 {
		t.Fatalf("disabled limit=%d", applied)
	}
}

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

func TestGoMemoryLimitSavedSettingWinsAtStart(t *testing.T) {
	store, runtime := newDefaultStore(t, deviceMemInfo)
	if err := os.WriteFile(store.path, []byte("75\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store.init()
	if runtime.limit != 75*1024*1024 {
		t.Fatalf("saved limit=%d", runtime.limit)
	}
	if err := store.remove(); err != nil || runtime.limit != 106924*1024/4 {
		t.Fatalf("remove: %d %v", runtime.limit, err)
	}
	// A corrupt setting falls back to the default.
	if err := os.WriteFile(store.path, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime.limit = math.MaxInt64
	store.init()
	if runtime.limit != 106924*1024/4 {
		t.Fatalf("corrupt setting limit=%d", runtime.limit)
	}
}

func TestGoMemoryLimitEnvironmentReplacesDefault(t *testing.T) {
	store, runtime := newDefaultStore(t, deviceMemInfo)
	runtime.limit = 40 * 1024 * 1024 // GOMEMLIMIT=40MiB in the environment
	store.init()
	if runtime.limit != 40*1024*1024 {
		t.Fatalf("environment limit replaced: %d", runtime.limit)
	}
	if err := store.set(60); err != nil {
		t.Fatal(err)
	}
	if err := store.remove(); err != nil || runtime.limit != 40*1024*1024 {
		t.Fatalf("remove: %d %v", runtime.limit, err)
	}
}

func TestGoMemoryLimitWithoutMemInfoStaysUnlimited(t *testing.T) {
	store, runtime := newDefaultStore(t, "")
	store.init()
	if runtime.limit != math.MaxInt64 {
		t.Fatalf("limit=%d", runtime.limit)
	}
}

func TestGoMemoryLimitFailureDoesNotChangeRuntime(t *testing.T) {
	dir := t.TempDir()
	applied := int64(75 * 1024 * 1024)
	store := goMemoryLimitStore{path: filepath.Join(dir, "missing", "limit"),
		apply: func(n int64) int64 { applied = n; return 0 }}
	if err := store.set(90); err == nil {
		t.Fatal("missing parent accepted")
	}
	// A nonempty directory also exercises failure after the temporary write.
	store.path = filepath.Join(dir, "destination")
	if err := os.Mkdir(store.path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.path, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.set(90); err == nil {
		t.Fatal("rename over directory accepted")
	}
	if err := store.remove(); err == nil {
		t.Fatal("nonempty directory removed")
	}
	if applied != 75*1024*1024 {
		t.Fatalf("failed operation changed runtime: %d", applied)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".GOMEMLIMIT-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files: %v %v", leftovers, err)
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

func TestGoMemoryLimitConcurrentSaveMatchesApplied(t *testing.T) {
	var applied int64
	store := goMemoryLimitStore{path: filepath.Join(t.TempDir(), "limit"),
		apply: func(n int64) int64 { applied = n; return 0 }}
	var wait sync.WaitGroup
	for i := int64(50); i < 70; i++ {
		wait.Add(1)
		go func(n int64) {
			defer wait.Done()
			if err := store.set(n); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wait.Wait()
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || saved*1024*1024 != applied {
		t.Fatalf("saved=%s applied=%d err=%v", data, applied, err)
	}
}
