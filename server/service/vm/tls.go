package vm

import (
	"fmt"
	"os/exec"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
)

func (s *Service) SetTls(c *gin.Context) {
	var req proto.SetTlsReq
	var rsp proto.Response

	err := proto.ParseFormRequest(c, &req)
	if err != nil {
		rsp.ErrRsp(c, -1, fmt.Sprintf("invalid arguments: %s", err))
		return
	}

	if req.Enabled {
		err = enableTls()
	} else {
		err = disableTls()
	}

	if err != nil {
		log.Errorf("failed to set TLS: %s", err)
		rsp.ErrRsp(c, -2, "operation failed")
		return
	}

	rsp.OkRsp(c)

	_ = exec.Command("sh", "-c", "/etc/init.d/S95nanokvm restart").Run()
}

func enableTls() error {
	conf, err := config.Read()
	if err != nil {
		return err
	}
	return enableTlsConfig(conf, config.Write, utils.EnsureServerCertificate)
}

func enableTlsConfig(conf *config.Config, writeConfig func(*config.Config) error, ensureCertificate func(string, string) error) error {
	if conf.Cert.Crt == "" {
		conf.Cert.Crt = "/etc/kvm/server.crt"
	}
	if conf.Cert.Key == "" {
		conf.Cert.Key = "/etc/kvm/server.key"
	}
	if err := ensureCertificate(conf.Cert.Crt, conf.Cert.Key); err != nil {
		return err
	}
	conf.Proto = "https"
	if err := writeConfig(conf); err != nil {
		return err
	}
	return nil
}

func disableTls() error {
	conf, err := config.Read()
	if err != nil {
		return err
	}

	conf.Proto = "http"

	if err := config.Write(conf); err != nil {
		return err
	}

	return nil
}
