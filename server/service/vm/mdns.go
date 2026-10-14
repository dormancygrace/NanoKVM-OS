package vm

import (
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"os"
	"os/exec"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const AvahiDaemonPid = "/run/avahi-daemon/pid"

func (s *Service) GetMdnsState(c *gin.Context) {
	var rsp proto.Response

	pid := getAvahiDaemonPid()

	rsp.OkRspWithData(c, &proto.GetMdnsStateRsp{
		Enabled: pid != "",
	})
}

func (s *Service) EnableMdns(c *gin.Context) {
	setMdns(c, true)
}

func (s *Service) DisableMdns(c *gin.Context) {
	setMdns(c, false)
}

func getAvahiDaemonPid() string {
	if _, err := os.Stat(AvahiDaemonPid); err != nil {
		return ""
	}

	content, err := os.ReadFile(AvahiDaemonPid)
	if err != nil {
		log.Errorf("failed to read mdns pid: %s", err)
		return ""
	}

	return strings.ReplaceAll(string(content), "\n", "")
}

// Keep persistent policy separate from APK-owned OpenRC service files.
func setMdns(c *gin.Context, enabled bool) {
	var rsp proto.Response
	action := "stop"
	marker := "/etc/kvm/mdns_disabled"
	var err error
	if enabled {
		action = "start"
		err = os.Remove(marker)
		if os.IsNotExist(err) {
			err = nil
		}
	} else {
		err = os.WriteFile(marker, []byte("1\n"), 0600)
	}
	if err == nil {
		argv := oomscore.Unprotected("rc-service", "avahi-daemon", action)
		err = exec.Command(argv[0], argv[1:]...).Run()
	}
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to change mDNS state")
		return
	}
	rsp.OkRsp(c)
}
