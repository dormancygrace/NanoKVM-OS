package vm

import "testing"

func TestActiveSwapParsingKeepsDevicesIndependent(t *testing.T) {
	swaps := parseActiveSwaps("Filename\tType\tSize\tUsed\tPriority\n/dev/zram0 partition 65532 8192 100\n/swapfile file 262140 4096 10\n/dev/sda2 partition 99999 0 -2\n")
	if !swaps["/dev/zram0"].Enabled || swaps["/dev/zram0"].SizeMiB != 64 || swaps["/dev/zram0"].UsedBytes != 8*1024*1024 {
		t.Fatal(swaps)
	}
	if swaps["/swapfile"].SizeMiB != 256 || swaps["/swapfile"].Priority != 10 {
		t.Fatal(swaps)
	}
	inactive := parseActiveSwaps("Filename Type Size Used Priority\n/dev/zram0 partition 65532 0 100\n")
	if inactive["/swapfile"].Enabled {
		t.Fatal("An existing /swapfile must not imply an active SD swap")
	}
}

// Matches S38memory: the shared 144 MiB region lends Linux what 2160p capture
// leaves of it, so the size is that of the earlier 104 MiB carveout.
func TestZramAutoSize(t *testing.T) {
	for _, tc := range []struct {
		meminfo string
		want    int64
	}{
		{"MemTotal: 240984 kB\nCmaTotal: 147456 kB\n", 65},
		{"MemTotal: 199840 kB\nCmaTotal: 0 kB\n", 97},
		{"MemTotal: 133120 kB\n", 65},
		{"MemTotal: 196608 kB\nCmaTotal: 65536 kB\n", 64},
	} {
		if got := zramAutoMiB(parseMemoryCounters(tc.meminfo)); got != tc.want {
			t.Errorf("%q: %d MiB, want %d", tc.meminfo, got, tc.want)
		}
	}
}

func TestMemoryCountersAndSwapValidation(t *testing.T) {
	counters := parseMemoryCounters("MemTotal: 193712 kB\nMemAvailable: 142644 kB\nSwapTotal: 65532 kB\nInvalid: unknown kB\n")
	if counters["MemTotal"] != 193712*1024 || counters["MemAvailable"] != 142644*1024 {
		t.Fatal(counters)
	}
	for _, req := range []memorySwapRequest{{Kind: "zram", SizeMiB: -1}, {Kind: "sd", SizeMiB: 1}, {Kind: "zram", SizeMiB: 512}, {Kind: "/dev/sda", SizeMiB: 256}} {
		if validSwapRequest(req) {
			t.Fatalf("accepted unsupported request: %+v", req)
		}
	}
}
