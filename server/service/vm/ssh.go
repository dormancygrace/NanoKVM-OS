package vm

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	SSHScript   = "/etc/init.d/S50sshd"
	SSHStopFlag = "/etc/kvm/ssh_stop"
)

func (s *Service) GetSSHState(c *gin.Context) {
	var rsp proto.Response

	enabled := isSSHEnabled()
	rsp.OkRspWithData(c, &proto.GetSSHStateRsp{
		Enabled: enabled,
	})
}

func (s *Service) EnableSSH(c *gin.Context) {
	var rsp proto.Response
	var req proto.EnableSSHReq
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "a new root password is required")
		return
	}
	password, err := utils.DecodeDecrypt(req.Password)
	if err != nil || strings.EqualFold(strings.TrimSpace(password), "root") {
		rsp.ErrRsp(c, -1, "invalid root password")
		return
	}
	if err := authn.ValidatePassword(password); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if err := changeRootPassword(password); err != nil {
		log.Errorf("failed to change root password before enabling SSH: %s", err)
		rsp.ErrRsp(c, -1, "could not set root password")
		return
	}

	command := fmt.Sprintf("%s permanent_on", SSHScript)
	err = exec.Command("sh", "-c", command).Run()
	if err != nil {
		log.Errorf("failed to run SSH script: %s", err)
		rsp.ErrRsp(c, -1, "operation failed")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("SSH enabled")
}

func changeRootPassword(password string) error {
	cmd := exec.Command("passwd", "root")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdin.Close() }()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	if _, err = io.WriteString(stdin, password+"\n"); err != nil {
		return err
	}
	// BusyBox passwd reads the confirmation after updating its prompt.
	time.Sleep(100 * time.Millisecond)
	if _, err = io.WriteString(stdin, password+"\n"); err != nil {
		return err
	}
	return cmd.Wait()
}

func (s *Service) DisableSSH(c *gin.Context) {
	var rsp proto.Response

	command := fmt.Sprintf("%s permanent_off", SSHScript)
	err := exec.Command("sh", "-c", command).Run()
	if err != nil {
		log.Errorf("failed to run SSH script: %s", err)
		rsp.ErrRsp(c, -1, "operation failed")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("SSH disabled")
}

func isSSHEnabled() bool {
	_, err := os.Stat(SSHStopFlag)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
	}

	return false
}
