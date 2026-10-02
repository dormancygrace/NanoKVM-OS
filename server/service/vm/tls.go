package vm

import (
	"fmt"
	"os/exec"
	"time"

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

	// The restart stops this process. Deliver the confirmation first and
	// restart only after the handler has returned, so the browser learns the
	// change was accepted instead of seeing a dropped connection.
	c.Writer.Flush()
	go func() {
		time.Sleep(tlsRestartDelay)
		_ = exec.Command("sh", "-c", "/etc/init.d/S95nanokvm restart").Run()
	}()
}

const tlsRestartDelay = time.Second

func enableTls() error {
	conf, err := config.Read()
	if err != nil {
		return err
	}
	return enableTlsConfig(conf, writeTLSConfig, utils.EnsureServerCertificate)
}

// writeTLSConfig persists only the TLS keys, leaving the rest of server.yaml
// untouched.
func writeTLSConfig(conf *config.Config) error {
	return config.UpdateTLS(conf.Proto, conf.Cert)
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

	return writeTLSConfig(conf)
}
