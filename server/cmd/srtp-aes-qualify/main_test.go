package main

import "testing"

func TestHardwareExposureBudget(t *testing.T) {
	for _, tc := range []struct {
		list            string
		packets, rounds int
		valid           bool
	}{
		{"1188", 2000, 3, true},
		{"512,768,1188", 500, 3, true},
		{"512,768,1188", 1007, 3, true},
		{"512,768,1188", 1008, 3, false},
		{"512", int(^uint(0) >> 1), 3, false},
		{"512", -1, 1, false},
		{"512", 1, 0, false},
		{"512", 1, 4, false},
		{"", 1, 1, false},
		{"511", 1, 1, false},
		{"4097", 1, 1, false},
		{"512,512", 1, 1, false},
		{"512,", 1, 1, false},
		{"512,513,514,515,516,517,518,519,520", 1, 1, false},
	} {
		_, err := benchmarkPayloads(tc.list, tc.packets, tc.rounds)
		if (err == nil) != tc.valid {
			t.Errorf("list=%q packets=%d rounds=%d: error=%v", tc.list, tc.packets, tc.rounds, err)
		}
	}
}
