package netbird

import (
	"net"
	"sync"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type Service struct{}

var operationMutex sync.Mutex

func NewService() *Service { return &Service{} }

func beginOperation(c *gin.Context, rsp *proto.Response) bool {
	if operationMutex.TryLock() {
		return true
	}
	rsp.ErrRsp(c, -1, "another NetBird operation is in progress")
	return false
}

func (s *Service) Install(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if !isInstalled() {
		if err := apkpkg.InstallTagged(
			"edgecommunity",
			"https://dl-cdn.alpinelinux.org/alpine/edge/community",
			"netbird", "netbird-openrc",
		); err != nil {
			rsp.ErrRsp(c, -1, "NetBird installation failed: "+err.Error())
			return
		}
	}
	if err := NewCli().Start(); err != nil {
		rsp.ErrRsp(c, -1, "NetBird was installed but could not start: "+err.Error())
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
		rsp.ErrRsp(c, -1, "NetBird could not be stopped; package was kept: "+err.Error())
		return
	}
	if err := apkpkg.Run("remove", "netbird-openrc"); err != nil {
		rsp.ErrRsp(c, -1, "NetBird OpenRC removal failed: "+err.Error())
		return
	} else if err = apkpkg.Run("remove", "netbird"); err != nil {
		rsp.ErrRsp(c, -1, "NetBird removal failed: "+err.Error())
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
		rsp.ErrRsp(c, -1, "NetBird start failed: "+err.Error())
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
		rsp.ErrRsp(c, -1, "NetBird restart failed: "+err.Error())
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
		rsp.ErrRsp(c, -1, "NetBird stop failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) Login(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	url, err := NewCli().Login()
	if err != nil {
		rsp.ErrRsp(c, -1, "NetBird login failed: "+err.Error())
		return
	}
	rsp.OkRspWithData(c, &proto.LoginNetbirdRsp{URL: url})
	log.Debugf("NetBird login URL issued: %t", url != "")
}

func (s *Service) Down(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := NewCli().Down(); err != nil {
		rsp.ErrRsp(c, -1, "NetBird down failed: "+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) GetStatus(c *gin.Context) {
	var rsp proto.Response
	if !isInstalled() {
		rsp.OkRspWithData(c, &proto.GetNetbirdStatusRsp{State: proto.NetbirdNotInstall})
		return
	}
	cli := NewCli()
	running, err := cli.ServiceRunning()
	if err != nil {
		rsp.ErrRsp(c, -1, "NetBird service status failed: "+err.Error())
		return
	}
	if !running {
		rsp.OkRspWithData(c, &proto.GetNetbirdStatusRsp{
			State: proto.NetbirdNotRunning, Version: installedVersion(),
		})
		return
	}
	status, err := cli.Status()
	if err != nil {
		rsp.ErrRsp(c, -1, "NetBird status unavailable: "+err.Error())
		return
	}
	state := proto.NetbirdNotLogin
	if status.Management.Connected && status.Signal.Connected {
		state = proto.NetbirdRunning
	} else if status.Management.URL != "" {
		state = proto.NetbirdStopped
	}
	version := status.DaemonVersion
	if version == "" {
		version = installedVersion()
	}
	rsp.OkRspWithData(c, &proto.GetNetbirdStatusRsp{
		State: state, Name: status.FQDN, IP: getIPv4(status.IP), Version: version,
	})
}

func getIPv4(value string) string {
	if ip, _, err := net.ParseCIDR(value); err == nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
		return ""
	}
	ip := net.ParseIP(value)
	if ip != nil && ip.To4() != nil {
		return ip.String()
	}
	return ""
}
