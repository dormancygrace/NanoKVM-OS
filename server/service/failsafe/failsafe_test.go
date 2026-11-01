package failsafe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrashClassification(t *testing.T) {
	for _, tc := range []struct {
		name, record string
		want         bool
	}{
		{"video-cgo", "950 2 ready\nSIGSEGV\ngoroutine 17 [syscall]:\n_Cfunc_kvmv_read()\n", true},
		{"native-thread", "950 139 ready\n", true},
		{"video-status", "950 2 ready\nfatal error: signal\ngoroutine 1 [syscall]:\n_Cfunc_get_mjpeg_chroma_status()\n", true},
		{"init-native-thread", "950 134 initializing\n", true},
		{"unrelated-panic", "950 2 ready\npanic: foo\ngoroutine 1 [running]:\nmain.run()\n\ngoroutine 17 [syscall]:\n_Cfunc_kvmv_read()\n", false},
		{"oom", "950 137 ready\n", false},
		{"clean", "950 0 ready\n", false},
		{"term", "950 143 ready\n", false},
		{"before-video", "950 139 \n", false},
		{"old", "399 139 ready\n", false},
		{"future", "1001 139 ready\n", false},
		{"invalid", "no 139 ready\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recentVideoCrash(tc.record, 1000); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestThresholdLatchAndClear(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("exit-%d.txt", i)), []byte("950 139 ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
		g, err := load(dir, 1000, nil)
		if err != nil {
			t.Fatal(err)
		}
		if g.status().Active != (i == 3) {
			t.Fatalf("threshold at %d: %+v", i, g.status())
		}
	}
	g, err := load(dir, 10000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !g.status().Active {
		t.Fatal("restart/age cleared the latch")
	}
	if err := g.clear(); err != nil {
		t.Fatal(err)
	}
	g, err = load(dir, 10000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.status().Active {
		t.Fatal("retry did not clear the latch")
	}
	// Malformed or overlarge records cannot activate recovery.
	for i := 1; i <= 8; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("exit-%d.txt", i)), []byte("950 139 ready\n"+strings.Repeat("x", maxRecordBytes)), 0600)
	}
	g, err = load(dir, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.status().Active {
		t.Fatal("oversized records counted")
	}
}
