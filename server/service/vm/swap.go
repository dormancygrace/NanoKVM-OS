package vm

import (
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

// Preserve the legacy API while using active state and targeted swap operations.
func (s *Service) GetSwap(c *gin.Context) {
	var rsp proto.Response
	state, err := readMemoryStatus()
	if err != nil {
		rsp.ErrRsp(c, -1, "Failed to read swap state")
		return
	}
	size := int64(0)
	if state.SD.Enabled {
		size = state.SD.SizeMiB
	}
	rsp.OkRspWithData(c, &proto.GetSwapRsp{Size: size})
}

func (s *Service) SetSwap(c *gin.Context) {
	var req proto.SetSwapReq
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "Invalid arguments")
		return
	}
	size := req.Size
	if size == 0 {
		size = 256
	}
	err := applyMemorySwap(memorySwapRequest{Kind: "sd", Enabled: req.Size != 0, SizeMiB: size})
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRsp(c)
}
