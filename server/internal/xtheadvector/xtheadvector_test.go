package xtheadvector

import "testing"

func TestISAHasExtension(t *testing.T) {
	for _, c := range []struct {
		cpuinfo string
		want    bool
	}{
		{"isa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm_xtheadvector\n", true},
		{"processor\t: 0\nhart\t\t: 0\nisa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm_xtheadvector\nmmu\t\t: sv39\n", true},
		{"processor : 0\nisa\t: rv64imafdc_xtheadvector_zba\n", true},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc_xtheadvector\n", true},
		{"isa : rv64gc_xtheadvector\r\nisa : rv64gc_xtheadvector\n", true},
		// Every hart must have it.
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc\n", false},
		// A whole extension name, not a prefix or suffix of one.
		{"isa : rv64imafdc_xtheadvectorx\n", false},
		{"isa : rv64imafdc_xthead\n", false},
		// The standard V extension is not XTheadVector.
		{"isa : rv64imafdcv\n", false},
		{"isa\t\t: rv64imafdc_zicntr_zicsr_zifencei_zihpm\n", false},
		// Only the "isa" key counts.
		{"isa extensions : xtheadvector\n", false},
		{"processor : 0\nhart : 0\n", false},
		{"", false},
	} {
		if got := isaHasExtension([]byte(c.cpuinfo), "xtheadvector"); got != c.want {
			t.Errorf("%q: %v, want %v", c.cpuinfo, got, c.want)
		}
	}
}
