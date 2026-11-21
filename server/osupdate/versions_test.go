package osupdate

import "testing"

func TestCompareAPKVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"3.5.0", "3.5.0", 0},
		{"3.5.0", "3.4.9", 1},
		{"3.10.0", "3.9.0", 1},
		{"3.5.0_beta1", "3.5.0", -1},
		{"3.5.0_beta2", "3.5.0_beta10", -1},
		{"3.5.0_rc1", "3.5.0_beta3", 1},
		{"3.5.0", "3.5", 1},
		{"3.5.0-r1", "3.5.0-r0", 1},
		{"3.5.0_p1", "3.5.0", 1},
		{"1.0a", "1.0", 1},
		{"3.0_alpha1-r1", "3.0-r0", -1},
	} {
		if got := compareAPKVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareAPKVersions(c.b, c.a); got != -c.want {
			t.Errorf("%s vs %s: %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
