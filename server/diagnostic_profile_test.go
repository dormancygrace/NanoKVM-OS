//go:build nanokvm_profile

package main

import (
	"testing"
	"time"
)

func TestDiagnosticDurationIsBounded(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", 30}, {"bad", 30}, {"-1", 30}, {"121", 30}, {"0", 0}, {"120", 120}, {"15", 15},
	} {
		t.Setenv("NANOKVM_CPU_PROFILE_SECONDS", tc.value)
		if got := diagnosticSeconds("NANOKVM_CPU_PROFILE_SECONDS", 30, 120); got != time.Duration(tc.want)*time.Second {
			t.Fatalf("value %q: got %s", tc.value, got)
		}
	}
}
