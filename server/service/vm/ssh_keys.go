package vm

import (
	"NanoKVM-Server/internal/sshkeys"
	"NanoKVM-Server/proto"
	"errors"
	"github.com/gin-gonic/gin"
)

func (s *Service) ImportSSHKeys(c *gin.Context) {
	importSSHKeys(c, "/root")
}

func importSSHKeys(c *gin.Context, home string) {
	var req struct {
		Keys string `json:"keys"`
	}
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid SSH key import request")
		return
	}
	result, err := sshkeys.Import(home, req.Keys)
	if errors.Is(err, sshkeys.ErrInvalid) {
		rsp.ErrRsp(c, -1, "sshKeys.invalid")
		return
	}
	if err != nil {
		rsp.ErrRsp(c, -2, "sshKeys.failed")
		return
	}
	rsp.OkRspWithData(c, result)
}
