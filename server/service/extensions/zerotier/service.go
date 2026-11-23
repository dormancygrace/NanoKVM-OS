package zerotier

import (
	"errors"
	"fmt"
	"sync"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/extensions/apkpkg"
	"NanoKVM-Server/service/extensions/vpn"
	"github.com/gin-gonic/gin"
)

// PackageName is the NanoKVM add-on: ZeroTier's MPL-2.0 agent built for
// musl, without the source-available network controller.
const PackageName = "nanokvm-zerotier"

type Service struct{}

var operationMutex sync.Mutex

func NewService() *Service { return &Service{} }

func beginOperation(c *gin.Context, rsp *proto.Response) bool {
	if operationMutex.TryLock() {
		return true
	}
	rsp.ErrRsp(c, -1, "another ZeroTier operation is in progress")
	return false
}

// zerotierState: the node identity (its private key and address), the joined
// networks and their settings, the local configuration and the caches. The
// device stays a member of each network until it is removed at the network's
// controller.
var zerotierState = []string{"/var/lib/zerotier-one"}

func (s *Service) Install(c *gin.Context) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if !isInstalled() {
		if err := apkpkg.InstallOnly(PackageName); err != nil {
			rsp.ErrRsp(c, -1, "ZeroTier installation failed: "+err.Error())
			return
		}
		// apk adds nothing for a package it records, even with its files
		// missing.
		if !isInstalled() {
			rsp.ErrRsp(c, -1, "ZeroTier installation failed: "+errMissingProgram.Error())
			return
		}
	}
	// Installed, not started: the page offers to start it, and starting
	// is what adds it to the boot runlevel.
	rsp.OkRsp(c)
}

func (s *Service) Uninstall(c *gin.Context) {
	purge := vpn.Purge(c)
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	// All under the package lock: the installed database, not the binary,
	// decides whether ZeroTier is installed, and the state is deleted only
	// once it is not. ZeroTier is stopped only once the removal can go ahead:
	// while another update holds the lock, it keeps running. A failed removal
	// starts it again, and puts it back in the boot runlevel, as it was.
	var finishErr error
	finish := func() error {
		finishErr = finishUninstall(purge)
		return finishErr
	}
	removed, err := apkpkg.RemoveAfter(NewCli().stopForRemoval, finish, PackageName)
	switch {
	case err == nil:
		rsp.OkRsp(c)
	case finishErr != nil && removed:
		rsp.ErrRsp(c, -1, "ZeroTier was removed, but "+finishErr.Error())
	case finishErr != nil:
		rsp.ErrRsp(c, -1, "ZeroTier uninstall failed: "+finishErr.Error())
	default:
		rsp.ErrRsp(c, -1, "ZeroTier removal failed: "+err.Error())
	}
}

// finishUninstall runs under the package lock once the package is gone or
// was never installed. A ZeroTier still there is another installation, whose
// identity and networks are not ours to delete.
func finishUninstall(purge bool) error {
	running, err := NewCli().ServiceRunning()
	if err != nil {
		return err
	}
	if running || isInstalled() {
		return errors.New("a ZeroTier that is not from the " + PackageName + " package is still installed; its identity and networks were kept")
	}
	if purge {
		if err = vpn.RemoveState(zerotierState...); err != nil {
			return fmt.Errorf("its identity and networks could not be deleted: %w", err)
		}
	}
	return nil
}

func (s *Service) Start(c *gin.Context) {
	s.run(c, "ZeroTier start failed: ", NewCli().Start)
}

func (s *Service) Restart(c *gin.Context) {
	s.run(c, "ZeroTier restart failed: ", NewCli().Restart)
}

func (s *Service) Stop(c *gin.Context) {
	s.run(c, "ZeroTier stop failed: ", NewCli().Stop)
}

func (s *Service) Join(c *gin.Context) {
	s.network(c, "ZeroTier could not join the network: ", NewCli().Join)
}

func (s *Service) Leave(c *gin.Context) {
	s.network(c, "ZeroTier could not leave the network: ", NewCli().Leave)
}

func (s *Service) run(c *gin.Context, failure string, action func() error) {
	var rsp proto.Response
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := action(); err != nil {
		rsp.ErrRsp(c, -1, failure+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) network(c *gin.Context, failure string, action func(string) error) {
	var rsp proto.Response
	var req proto.ZerotierNetworkReq
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid request")
		return
	}
	id, ok := ValidNetworkID(req.Network)
	if !ok {
		rsp.ErrRsp(c, -1, "a ZeroTier network ID is 16 hexadecimal digits")
		return
	}
	if !beginOperation(c, &rsp) {
		return
	}
	defer operationMutex.Unlock()
	if err := action(id); err != nil {
		rsp.ErrRsp(c, -1, failure+err.Error())
		return
	}
	rsp.OkRsp(c)
}

func (s *Service) GetStatus(c *gin.Context) {
	var rsp proto.Response
	if !isInstalled() {
		// A recorded package without its program is installed, and damaged:
		// it is not running, and the page offers Uninstall.
		state := proto.ZerotierNotInstall
		if recorded() {
			state = proto.ZerotierNotRunning
		}
		rsp.OkRspWithData(c, &proto.GetZerotierStatusRsp{State: state})
		return
	}
	cli := NewCli()
	running, err := cli.ServiceRunning()
	if err != nil {
		rsp.ErrRsp(c, -1, "ZeroTier service status failed: "+err.Error())
		return
	}
	if !running {
		rsp.OkRspWithData(c, &proto.GetZerotierStatusRsp{
			State: proto.ZerotierNotRunning, Version: installedVersion(),
		})
		return
	}
	info, err := cli.Info()
	if err != nil {
		// Started but not yet answering (it reads its identity first), or
		// crashed and waiting to be respawned.
		rsp.OkRspWithData(c, &proto.GetZerotierStatusRsp{
			State: proto.ZerotierConnecting, Version: installedVersion(),
		})
		return
	}
	networks, err := cli.Networks()
	if err != nil {
		rsp.ErrRsp(c, -1, "ZeroTier network list unavailable: "+err.Error())
		return
	}
	state := proto.ZerotierConnecting
	if info.Online {
		state = proto.ZerotierRunning
	}
	version := info.Version
	if version == "" {
		version = installedVersion()
	}
	rsp.OkRspWithData(c, &proto.GetZerotierStatusRsp{
		State: state, Address: info.Address, IP: firstIPv4(networks), Version: version, Networks: networks,
	})
}
