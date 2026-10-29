package xtheadvector

import "testing"

func TestISAHasExtension(t *testing.T) {
	for _, c := range []struct {
		cpuinfo string
		want    bool
	}{
		{"isa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm_xtheadvector\n", true},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc_xtheadvector\n", true},
		// Every hart must have it.
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc\n", false},
		// A whole extension name, not a prefix or suffix of one.
		{"isa : rv64imafdc_xtheadvectorx\n", false},
		{"isa : rv64imafdc_xthead\n", false},
		{"processor : 0\nhart : 0\n", false},
		{"", false},
	} {
		if got := isaHasExtension([]byte(c.cpuinfo), "xtheadvector"); got != c.want {
			t.Errorf("%q: %v, want %v", c.cpuinfo, got, c.want)
		}
	}
}
