// Package resolvconf asks openresolv about the records it holds.
package resolvconf

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Has reports whether openresolv holds a record under key ("nkos.base", a
// VPN interface). It asks resolvconf itself: the directory of its records
// is not the same in every version (/run/resolvconf/keys in 3.17, the one
// in the image), and a path that is not there would always say "no".
func Has(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "resolvconf", "-i", key).Output()
	return err == nil && listed(out, key)
}

// listed: "resolvconf -i KEY" prints the matching keys; for none it says so
// on stderr and still exits 0.
func listed(out []byte, key string) bool {
	for _, k := range strings.Fields(string(out)) {
		if k == key {
			return true
		}
	}
	return false
}
