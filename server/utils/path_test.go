package utils

import "testing"

func TestJoinWithin(t *testing.T) {
	for _, tc := range []struct {
		dir, name, want string
	}{
		{"/data", "image.iso", "/data/image.iso"},
		{"/data/", "sub/image.iso", "/data/sub/image.iso"},
		{"/data", "sub/../image.iso", "/data/image.iso"},
		{"/data", "/image.iso", "/data/image.iso"},
	} {
		got, err := JoinWithin(tc.dir, tc.name)
		if err != nil || got != tc.want {
			t.Errorf("JoinWithin(%q, %q) = %q, %v; want %q", tc.dir, tc.name, got, err, tc.want)
		}
	}
	for _, name := range []string{"", ".", "..", "../etc/passwd", "sub/../..", "/../data2/x"} {
		if got, err := JoinWithin("/data", name); err == nil {
			t.Errorf("JoinWithin(/data, %q) = %q; want an error", name, got)
		}
	}
	if got, err := JoinWithin("/data", "../data2"); err == nil {
		t.Errorf("sibling directory with the same prefix was accepted: %q", got)
	}
}
