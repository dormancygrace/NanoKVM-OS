package config

import "testing"

func TestStunDefaultSet(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", DefaultStun},
		{"stun.l.google.com:19302", DefaultStun},
		{"stun.example:3478", "stun.example:3478"},
		{"disable", "disable"},
		{DefaultStun, DefaultStun},
	} {
		if got := stunOrDefault(tc.in); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.in, got, tc.want)
		}
	}
}
