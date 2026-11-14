package vm

import (
	"errors"
	"testing"
)

// The host is silent only when every request went out and none was
// answered: a request that could not be sent is a question not asked.
func TestARPVerdict(t *testing.T) {
	for name, c := range map[string]struct {
		sent     int
		answered bool
		err      error
		want     string
	}{
		"an answer":                     {1, true, nil, hostAlive},
		"three requests, no answer":     {arpRequests, false, nil, hostSilent},
		"no request could be sent":      {0, false, errors.New("sending a request: no buffer space available"), hostUnknown},
		"the second send failed":        {1, false, errors.New("sending a request: network is down"), hostUnknown},
		"fewer requests than asked for": {arpRequests - 1, false, nil, hostUnknown},
		"no socket (not root, no usb0)": {0, false, errors.New("operation not permitted"), hostUnknown},
		"an answer, then a failed send": {2, true, errors.New("late"), hostAlive},
	} {
		if got, _ := arpVerdict(c.sent, c.answered, c.err); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}
