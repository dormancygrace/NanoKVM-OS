package logs

import (
	"strings"
	"testing"
)

func TestLogVectorISARecords(t *testing.T) {
	cases := []struct {
		info string
		want bool
	}{
		{"", false}, {"isa : rv64imafdcv\n", false},
		{"isa : rv64imafdc_xtheadvector\n", true},
		{"isa : rv64imafdc_xtheadvectorx\n", false},
		{"isa extensions : xtheadvector\n", false},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc\n", false},
		{"isa : rv64imafdc_xtheadvector\nisa : rv64imafdc_xtheadvector\n", true},
		{"processor : 0\nisa\t: rv64imafdc_xtheadvector_zba\n", true},
	}
	for _, tc := range cases {
		if got := logCPUHasVector([]byte(tc.info)); got != tc.want {
			t.Errorf("info%q:got%v want%v", tc.info, got, tc.want)
		}
	}
}

func TestShortRepetitiveLogRouting(t *testing.T) {
	for _, s := range []string{"", "password=x", strings.Repeat("a", 95), strings.Repeat("pxk", 32), strings.Repeat("private_kex", 4)} {
		if !preferScalarLogLine(s) {
			t.Errorf("must retain scalar search: %q", s)
		}
	}
	for _, s := range []string{"[info] capture ready at 2560x1440", strings.Repeat("a", 257)} {
		if preferScalarLogLine(s) {
			t.Errorf("unexpected scalar route: %q", s)
		}
	}
}

func TestScalarKeywordCandidates(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 15, 16, 31, 32, 64, 95, 128} {
		for _, word := range append(append([]string{}, sensitiveWords...), "", "x", "abca", "aaaaaaabaaaaaa") {
			for _, pattern := range []string{"a", "pxk", "paaaaaad", "passwoxd"} {
				base := strings.Repeat(pattern, (n+len(pattern)-1)/len(pattern))[:n]
				for _, s := range []string{base, base + word, word + base, base + word + base} {
					if got := containsKeywordScalar(s, word); got != strings.Contains(s, word) {
						t.Fatalf("s%q word%q got%v", s, word, got)
					}
				}
			}
		}
	}
}
