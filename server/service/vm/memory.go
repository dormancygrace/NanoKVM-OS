package vm

import (
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func (s *Service) SetMemoryLimit(c *gin.Context) {
	var req proto.SetMemoryLimitReq
	var rsp proto.Response

	err := proto.ParseFormRequest(c, &req)
	if err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if req.Enabled {
		err = utils.SetGoMemLimit(req.Limit)
	} else {
		err = utils.DelGoMemLimit()
	}

	if err != nil {
		rsp.ErrRsp(c, -2, "failed to set memory limit")
		return
	}

	s.GetMemoryLimit(c)
	log.Debugf("set memory limit successful, enabled: %t, limit: %d", req.Enabled, req.Limit)
}

func (s *Service) GetMemoryLimit(c *gin.Context) {
	var rsp proto.Response
	data := &proto.GetMemoryLimitRsp{
		Default: utils.GoMemLimitDefaultMiB(),
		Current: utils.GoMemLimitCurrentMiB(),
		Minimum: utils.GoMemLimitMinimumMiB,
	}
	if utils.IsGoMemLimitExist() {
		limit, err := utils.GetGoMemLimit()
		if err != nil {
			rsp.ErrRsp(c, -1, "failed to get memory limit")
			return
		}
		data.Enabled, data.Limit = true, limit
	}
	rsp.OkRspWithData(c, data)
}
