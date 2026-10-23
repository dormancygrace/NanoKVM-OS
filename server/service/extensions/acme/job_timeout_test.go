package acme

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A timeout ends the whole acme.sh process tree. Its children (curl, a DNS
// hook) hold the output pipe; killing acme.sh alone left Wait blocked on
// them, and the job (and every later one) stuck for as long as they ran.
func TestTimeoutKillsTheProcessGroup(t *testing.T) {
	useTempTree(t)
	if err := os.MkdirAll(binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nsleep 60 &\nsleep 60\n"
	if err := os.WriteFile(filepath.Join(binDir(), "acme.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runStep(ctx, &tailBuffer{}, nil, step{args: []string{"--cron"}}); err == nil {
		t.Fatal("a step past its deadline succeeded")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the step returned after %v", elapsed)
	}
}

func TestDNSWaitExtendsTheJobTimeout(t *testing.T) {
	steps := []step{{args: []string{"--issue", "--dns", "dns_cf", "--dnssleep", "1800"}}, {args: []string{"--install-cert"}}}
	if got := stepsTimeout(steps); got != jobTimeout+30*time.Minute {
		t.Fatalf("timeout %v, want %v", got, jobTimeout+30*time.Minute)
	}
	if got := stepsTimeout([]step{{args: []string{"--cron"}}}); got != jobTimeout {
		t.Fatalf("timeout without a DNS wait %v", got)
	}
}
