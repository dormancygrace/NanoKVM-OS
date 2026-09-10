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
