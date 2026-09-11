package vm

import (
	"NanoKVM-Server/config"
	"NanoKVM-Server/dashboard"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

func (s *Service) GetDashboard(c *gin.Context) {
	var rsp proto.Response
	var memory any
	if value, err := readMemoryStatus(); err == nil {
		memory = value
	}
	rsp.OkRspWithData(c, gin.H{"system": dashboard.Read(), "memory": memory, "hardware": config.GetInstance().Hardware.Version.String(), "application": getApplicationVersion(), "image": getImageVersion()})
}
