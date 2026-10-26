//go:build linux && riscv64 && !purego

package logs

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

func requireLogVector(t *testing.T) {
	t.Helper()
	if !logVectorAvailable() {
		t.Skip("XTheadVector unavailable or disabled")
	}
}

func TestLogTextGenericOverride(t *testing.T) {
	t.Setenv("NANOKVM_LOG_TEXT", "generic")
	if detectLogVector() {
		t.Fatal("generic override did not disable vectors")
	}
}

func TestLogVectorKeywordSearch(t *testing.T) {
	requireLogVector(t)
	words := append(append([]string{}, sensitiveWords...), "http", "", "a", "abca", "aaaaaaabaaaaaa")
	for _, n := range []int{0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 95, 127, 128, 129, 255, 256, 257, 1024} {
		for _, word := range words {
			for _, pattern := range []string{"a", "pxk", "paaaaaad", "passwoxd", "private_kex"} {
				base := strings.Repeat(pattern, (n+len(pattern)-1)/len(pattern))[:n]
				for _, s := range []string{base, base + word, word + base, "[info] " + base, "[info] " + base + word} {
					if got := containsKeywordVector(s, word); got != strings.Contains(s, word) {
						t.Fatalf("n%d word%q input%x got%v", n, word, s, got)
					}
				}
			}
		}
	}
}

//go:noinline
func needsScalar(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x7f || c < 0x20 && c != '\n' && c != '\t' {
			return true
		}
	}
	return false
}

//go:noinline
func indexScalar(s string, value byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == value {
			return i
		}
	}
	return -1
}

var needs = []struct {
	name string
	fn   func(string) bool
}{
	{"go", needsScalar}, {"m1", logNeedsM1}, {"m2", logNeedsM2}, {"m4", logNeedsM4}, {"m8", logNeedsM8},
}
var indexes = []struct {
	name string
	fn   func(string, byte) int
}{
	{"go", strings.IndexByte}, {"scalar", indexScalar}, {"m1", logIndexM1}, {"m2", logIndexM2}, {"m4", logIndexM4}, {"m8", logIndexM8},
}

type pairFunc func(string, byte, byte, int) int

var pairs = []struct {
	name string
	fn   pairFunc
}{
	{"m1", logPairM1}, {"m2", logPairM2}, {"m4", logPairM4}, {"m8", logPairM8},
}

func containsPair(s, sub string, pair pairFunc) bool {
	n := len(sub)
	if n == 0 {
		return true
	}
	if n == 1 {
		return strings.IndexByte(s, sub[0]) >= 0
	}
	if n > len(s) {
		return false
	}
	for fails := 0; len(s) >= n; fails++ {
		if fails == 8 {
			return strings.Contains(s, sub)
		}
		i := pair(s, sub[0], sub[n-1], n-1)
		if i < 0 {
			return false
		}
		if s[i:i+n] == sub {
			return true
		}
		s = s[i+1:]
	}
	return false
}
func alias(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

func TestLogVectorEveryByteAndTail(t *testing.T) {
	requireLogVector(t)
	cases := 0
	for _, n := range []int{0, 1, 7, 15, 16, 17, 31, 32, 33, 63, 64, 65, 95, 127, 128, 129, 255, 256, 257} {
		for off := 0; off < 8; off++ {
			raw := []byte(strings.Repeat("a", n+off+16))
			data := raw[off : off+n]
			for value := 0; value < 256; value++ {
				positions := []int{0, n / 2, n - 1}
				if n == 0 {
					positions = []int{-1}
				}
				for _, at := range positions {
					if at >= 0 {
						data[at] = byte(value)
					}
					s := alias(data)
					want := needsScalar(s)
					for _, f := range needs[1:] {
						if got := f.fn(s); got != want {
							t.Fatalf("needs %s n%d off%d at%d value%d got%v", f.name, n, off, at, value, got)
						}
					}
					for _, q := range []byte{byte(value), 0xff} {
						wantIndex := strings.IndexByte(s, q)
						for _, f := range indexes[2:] {
							if got := f.fn(s, q); got != wantIndex {
								t.Fatalf("index %s n%d off%d at%d value%d got%d want%d", f.name, n, off, at, value, got, wantIndex)
							}
						}
					}
					if at >= 0 {
						data[at] = 'a'
					}
					cases++
				}
			}
		}
	}
	t.Logf("%d byte/alignment/tail cases", cases)
}
func TestLogVectorSubstringPairs(t *testing.T) {
	requireLogVector(t)
	inputs := []string{"", "a", "password", "ordinary ascii log line", strings.Repeat("a", 1000), "текст\x00binary\xff"}
	state := uint64(0xc906)
	for i := 0; i < 1000; i++ {
		data := make([]byte, i%513)
		for j := range data {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			data[j] = byte(state)
		}
		inputs = append(inputs, string(data))
	}
	for _, s := range inputs {
		needles := []string{"", "a", "password", "abca", "р", "x\x00y"}
		if len(s) > 2 {
			needles = append(needles, s[len(s)/3:len(s)/3+2], s[len(s)-2:])
		}
		for _, sub := range needles {
			for _, f := range pairs {
				got := containsPair(s, sub, f.fn)
				want := strings.Contains(s, sub)
				if got != want {
					t.Fatalf("%s s=%x sub=%x got%v want%v", f.name, s, sub, got, want)
				}
			}
		}
	}
}
func TestLogVectorGuardPages(t *testing.T) {
	requireLogVector(t)
	page := os.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 3*page, syscall.PROT_NONE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(mem)
	middle := mem[page : 2*page]
	if err = syscall.Mprotect(middle, syscall.PROT_READ|syscall.PROT_WRITE); err != nil {
		t.Fatal(err)
	}
	for i := range middle {
		middle[i] = 'a'
	}
	for n := 0; n <= 257; n++ {
		for _, start := range []int{0, page - n} {
			data := middle[start : start+n]
			s := alias(data)
			for _, f := range needs[1:] {
				if f.fn(s) {
					t.Fatalf("guard needs %s n%d", f.name, n)
				}
			}
			for _, f := range indexes[2:] {
				if f.fn(s, 0xff) != -1 {
					t.Fatalf("guard index %s", f.name)
				}
			}
			for _, f := range pairs {
				for _, sub := range []string{"abca", "aaa", "a", ""} {
					if containsPair(s, sub, f.fn) != strings.Contains(s, sub) {
						t.Fatalf("guard pair %s", f.name)
					}
				}
			}
		}
	}
}
func TestLogVectorConcurrentState(t *testing.T) {
	requireLogVector(t)
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := []byte(strings.Repeat("a", 16384))
			data[len(data)-1] = 0xff
			s := alias(data)
			for i := 0; i < 400; i++ {
				for _, f := range needs[1:] {
					if !f.fn(s) {
						t.Errorf("state needs %s", f.name)
						return
					}
				}
				for _, f := range indexes[2:] {
					if f.fn(s, 0xff) != len(s)-1 {
						t.Errorf("state index %s", f.name)
						return
					}
				}
				if i%50 == 0 {
					runtime.Gosched()
					syscall.Kill(os.Getpid(), syscall.SIGURG)
				}
			}
		}()
	}
	wg.Wait()
}
