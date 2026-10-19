package vm

import "NanoKVM-Server/common"

// videoMemoryStatus is the video memory of the booted image. Current boot
// images have one reusable 144 MiB CMA region for every resolution, which
// Linux uses while video does not (platform/build.sh). A device updated from
// an earlier release keeps its fixed carveout (40, 56 or 104 MiB) until it
// restarts with the installed kernel.
type videoMemoryStatus struct {
	SizeMiB int  `json:"sizeMiB"`
	Shared  bool `json:"shared"`
}

func readVideoMemoryStatus() videoMemoryStatus {
	pool := common.BootedVideoPool()
	return videoMemoryStatus{SizeMiB: pool.MiB, Shared: pool.Reusable}
}
