package resolvconf

import "testing"

func TestListed(t *testing.T) {
	for out, want := range map[string]bool{"nkos.base\n": true, "nkos.base nv0123456789\n": true, "": false, "nkos.base.old\n": false} {
		if listed([]byte(out), "nkos.base") != want {
			t.Errorf("%q: want %v", out, want)
		}
	}
}
