package tailscale

import (
	"net"
	"sync"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type Service struct{}

const TailscalePath = "/usr/bin/tailscale"

var operationMutex sync.Mutex

func beginOperation(c *gin.Context, rsp *proto.Response) bool {
	if operationMutex.TryLock() {
		return true
	}
	rsp.ErrRsp(c, -1, "another Tailscale operation is in progress")
	return false
}

var StateMap = map[string]proto.TailscaleState{
	"NoState": proto.TailscaleNotRunning, "Starting": proto.TailscaleNotRunning,
	"NeedsLogin": proto.TailscaleNotLogin, "NeedsMachineAuth": proto.TailscaleNotLogin,
	"InUseOtherUser": proto.TailscaleNotLogin, "Running": proto.TailscaleRunning,
	"Stopped": proto.TailscaleStopped,
}

func NewService() *Service { return &Service{} }

func (s *Service) Install(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if !isInstalled() {
		if err := apkpkg.InstallTagged("edgecommunity", "https://dl-cdn.alpinelinux.org/alpine/edge/community", "tailscale", "tailscale-openrc"); err != nil {
			rsp.ErrRsp(c, -1, "Tailscale installation failed: "+err.Error())
			return
		}
	}
	if err := NewCli().Start(); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale was installed but could not start: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Uninstall(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if !isInstalled() {
		rsp.OkRsp(c)
		return
	}
	if err := NewCli().Stop(); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale could not be stopped; package was kept: "+err.Error())
		return
	}
	if err := apkpkg.Run("remove", "tailscale-openrc"); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale OpenRC removal failed: "+err.Error())
		return
	}
	if err := apkpkg.Run("remove", "tailscale"); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale removal failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Start(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := NewCli().Start(); err != nil {
		rsp.ErrRsp(c, -1, "start failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Restart(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := NewCli().Restart(); err != nil {
		rsp.ErrRsp(c, -1, "restart failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Stop(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := NewCli().Stop(); err != nil {
		rsp.ErrRsp(c, -1, "stop failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Up(c *gin.Context) {
	var rsp proto.Response
	if err := NewCli().Up(); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale up failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Down(c *gin.Context) {
	var rsp proto.Response
	if err := NewCli().Down(); err != nil {
		rsp.ErrRsp(c, -1, "Tailscale down failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Login(c *gin.Context) {
	var rsp proto.Response
	cli := NewCli()
	status, err := cli.Status()
	if err != nil {
		if err = cli.Start(); err == nil {
			status, err = cli.Status()
		}
	}
	if err != nil {
		rsp.ErrRsp(c, -1, "unknown status: "+err.Error())
		return
	}
	if status.BackendState == "Running" {
		rsp.OkRspWithData(c, &proto.LoginTailscaleRsp{})
		return
	}
	url, err := cli.Login()
	if err != nil {
		rsp.ErrRsp(c, -2, "login failed: "+err.Error())
		return
	}
	rsp.OkRspWithData(c, &proto.LoginTailscaleRsp{Url: url})
	log.Debugf("Tailscale login URL issued: %t", url != "")
}

func (s *Service) Logout(c *gin.Context) {
	var rsp proto.Response
	if err := NewCli().Logout(); err != nil {
		rsp.ErrRsp(c, -1, "logout failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) GetStatus(c *gin.Context) {
	var rsp proto.Response
	if !isInstalled() {
		rsp.OkRspWithData(c, &proto.GetTailscaleStatusRsp{State: proto.TailscaleNotInstall})
		return
	}
	status, err := NewCli().Status()
	if err != nil {
		rsp.OkRspWithData(c, &proto.GetTailscaleStatusRsp{State: proto.TailscaleNotRunning})
		return
	}
	state, ok := StateMap[status.BackendState]
	if !ok {
		rsp.ErrRsp(c, -1, "unknown state")
		return
	}
	ipv4 := ""
	for _, value := range status.Self.TailscaleIPs {
		if ip := net.ParseIP(value); ip != nil && ip.To4() != nil {
			ipv4 = ip.String()
		}
	}
	rsp.OkRspWithData(c, &proto.GetTailscaleStatusRsp{State: state, IP: ipv4, Name: status.Self.HostName, Account: status.CurrentTailnet.Name})
}
