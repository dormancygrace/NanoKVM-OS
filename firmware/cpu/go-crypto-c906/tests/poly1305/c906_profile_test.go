// Copyright 2026 The NanoKVM OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package poly1305

import (
	"bytes"
	"internal/profile"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// TestC906ProfileUnwind takes a CPU profile while hashing and checks that
// every sample inside the CRYPTOGAMS blocks functions unwinds to this test:
// they must not change the stack pointer behind Go's back. Builds without
// them (the upstream reference) have no such samples and skip.
func TestC906ProfileUnwind(t *testing.T) {
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		t.Skip("CPU profile unavailable:", err)
	}
	var key [32]byte
	msg := make([]byte, 16384+5)
	var out [16]byte
	for _, fast := range []bool{true, false} {
		old := useMisalignedLoads
		useMisalignedLoads = fast
		for end := time.Now().Add(time.Second); time.Now().Before(end); {
			Sum(&out, msg[5:], &key)
			Sum(&out, msg[:4096], &key)
		}
		useMisalignedLoads = old
	}
	pprof.StopCPUProfile()
	p, err := profile.Parse(&buf)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, s := range p.Sample {
		if len(s.Location) == 0 || len(s.Location[0].Line) == 0 {
			continue
		}
		leaf := s.Location[0].Line[0].Function.Name
		if !strings.Contains(leaf, "poly1305Blocks") {
			continue
		}
		seen[leaf]++
		ok := false
		for _, l := range s.Location {
			for _, ln := range l.Line {
				ok = ok || strings.HasSuffix(ln.Function.Name, "TestC906ProfileUnwind")
			}
		}
		if !ok {
			var names []string
			for _, l := range s.Location {
				for _, ln := range l.Line {
					names = append(names, ln.Function.Name)
				}
			}
			t.Fatalf("sample in %s does not unwind to the test: %s", leaf, strings.Join(names, " <- "))
		}
	}
	if len(seen) == 0 {
		t.Skip("no samples in CRYPTOGAMS blocks functions (reference build)")
	}
	t.Logf("PASS %v samples in the blocks functions unwind to the test", seen)
}
