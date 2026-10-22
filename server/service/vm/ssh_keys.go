package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/internal/sshkeys"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"errors"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"net/http"
)

func (s *Service) ImportSSHKeys(c *gin.Context) {
	importSSHKeys(c, "/root")
}

func importSSHKeys(c *gin.Context, home string) {
	var req struct {
		Keys     string `json:"keys"`
		Password string `json:"password"`
	}
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, sshkeys.MaxInput*6+4096)
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid SSH key import request")
		return
	}
	principal, ok := middleware.CurrentPrincipal(c)
	if !ok || principal.Role != authn.RoleAdmin {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	user, valid, err := authn.DefaultStore.Authenticate(principal.Username, req.Password)
	if err != nil || !valid || user.Role != authn.RoleAdmin {
		log.Infof("SSH key import denied: user=%q remote=%q", principal.Username, c.Request.RemoteAddr)
		rsp.ErrRsp(c, -3, "auth.invalidCurrentPassword")
		return
	}
	result, err := sshkeys.Import(home, req.Keys)
	log.Infof("SSH key import: user=%q remote=%q added=%d skipped=%d fingerprints=%v success=%t", principal.Username, c.Request.RemoteAddr, result.Added, result.Skipped, result.Fingerprints, err == nil)
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
