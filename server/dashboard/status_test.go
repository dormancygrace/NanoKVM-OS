package dashboard

import "testing"

func TestCPUCounterExcludesGuest(t *testing.T) {
	c := parseCPU("cpu  100 20 30 400 50 6 7 8 90 10\ncpu0 1 2 3 4")
	if c == nil || c.Total != 621 || c.Idle != 450 {
		t.Fatalf("bad accounting: %+v", c)
	}
	for _, bad := range []string{"", "cpu0 1 2 3 4", "cpu 1 2 nope 4"} {
		if parseCPU(bad) != nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
func TestMissingDataMountDoesNotReportRootSpace(t *testing.T) {
	mounts := mountedPaths("/dev/root / ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n")
	s := storage("/data", mounts["/data"])
	if s.Available || s.Total != 0 {
		t.Fatalf("unmounted /data appears available: %+v", s)
	}
}
