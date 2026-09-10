package kvmcapture

import (
	"NanoKVM-Server/common"
	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/mcp/capture"
	"NanoKVM-Server/service/vm"
)

func New() mcpservice.Snapshotter {
	return mcpcapture.NewWithCaptureLease(common.GetKvmVision(), func() (uint16, uint16) {
		common.CheckScreen()
		screen := common.GetScreen()
		return screen.Width, screen.Height
	}, vm.AcquireHdmiCaptureLeaseForRead)
}
