package vm

import (
	"context"
	"os/exec"
	"time"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/timeconfig"
	"github.com/gin-gonic/gin"
)

var deviceTime = timeconfig.Store{Dir: "/etc", Restart: func() error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	argv := oomscore.Unprotected("/etc/init.d/S49chronyd", "restart")
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}}

func (s *Service) GetDateTime(c *gin.Context) {
	var rsp proto.Response
	cfg, err := deviceTime.Read()
	if err != nil {
		rsp.ErrRsp(c, -1, "Cannot read date and time settings")
		return
	}
	synced, err := timeconfig.ChronySynchronized()
	rsp.OkRspWithData(c, gin.H{"config": cfg, "zones": timeconfig.Zones(), "now": time.Now().UnixMilli(),
		"synchronized": err == nil && synced, "daemon": "chrony"})
}

func (s *Service) SetDateTime(c *gin.Context) {
	var rsp proto.Response
	var cfg timeconfig.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		rsp.ErrRsp(c, -1, "Invalid date and time settings")
		return
	}
	if err := timeconfig.Validate(cfg); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if err := deviceTime.Save(cfg); err != nil {
		rsp.ErrRsp(c, -2, "Cannot apply date and time settings; previous configuration was restored where possible")
		return
	}
	s.GetDateTime(c)
}
