package vm

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// Only requests that went out unanswered are a silent host; a probe that
// could not run is not.
func TestARPProbeResult(t *testing.T) {
	exit := func(code string) error { return exec.Command("sh", "-c", "exit "+code).Run() }
	for name, c := range map[string]struct {
		err, timeout error
		want         string
	}{
		"a reply":                 {nil, nil, hostAlive},
		"no reply":                {exit("1"), nil, hostSilent},
		"arping's own error":      {exit("2"), nil, hostUnknown},
		"arping not there":        {exec.Command("/nonexistent/arping").Run(), nil, hostUnknown},
		"out of time, then ended": {exit("1"), context.DeadlineExceeded, hostUnknown},
		"killed":                  {errors.New("signal: killed"), nil, hostUnknown},
	} {
		if got, _ := arpProbeResult(c.err, c.timeout); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}
