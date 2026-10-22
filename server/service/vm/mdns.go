package vm

import (
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

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

var (
	mdnsMarker   = "/etc/kvm/mdns_disabled"
	mdnsRunlevel = "/etc/runlevels/default/nanokvm-mdns"
	mdnsRun      = func(argv ...string) error { return exec.Command(argv[0], argv[1:]...).Run() }
)

// Keep persistent policy separate from APK-owned OpenRC service files.
func setMdns(c *gin.Context, enabled bool) {
	var rsp proto.Response
	action := "stop"
	var err error
	if enabled {
		action = "start"
		err = os.Remove(mdnsMarker)
		if os.IsNotExist(err) {
			err = nil
		}
	} else {
		err = os.WriteFile(mdnsMarker, []byte("1\n"), 0600)
	}
	if err == nil {
		err = syncMdnsRunlevel(enabled)
	}
	if err == nil {
		err = mdnsRun(oomscore.Unprotected("rc-service", "avahi-daemon", action)...)
	}
	if err != nil {
		rsp.ErrRsp(c, -1, "failed to change mDNS state")
		return
	}
	rsp.OkRsp(c)
}

// syncMdnsRunlevel keeps nanokvm-mdns in the default runlevel only while mDNS
// is on. Started with mDNS off it would only find the marker and stop, at
// the cost of an OpenRC service start at every boot.
func syncMdnsRunlevel(enabled bool) error {
	_, err := os.Lstat(mdnsRunlevel)
	present := err == nil
	switch {
	case enabled && !present:
		return mdnsRun("rc-update", "add", "nanokvm-mdns", "default")
	case !enabled && present:
		return mdnsRun("rc-update", "del", "nanokvm-mdns", "default")
	}
	return nil
}

// ReconcileMdnsAfterBoot runs ReconcileMdns once the boot is over: OpenRC is
// still starting the default runlevel when the server starts.
func ReconcileMdnsAfterBoot(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Second):
		ReconcileMdns()
	}
}

// ReconcileMdns makes the runlevel follow the marker once the server runs.
// A recovery restores /etc/kvm but not the runlevels, and an update finds the
// service in the runlevel whatever the setting: with mDNS on and the service
// missing, it also starts mDNS now rather than at the next boot.
func ReconcileMdns() {
	_, err := os.Stat(mdnsMarker)
	enabled := os.IsNotExist(err)
	if err != nil && !enabled {
		log.Warnf("mDNS setting unreadable: %v", err)
		return
	}
	_, err = os.Lstat(mdnsRunlevel)
	missing := err != nil
	if err := syncMdnsRunlevel(enabled); err != nil {
		log.Warnf("failed to update the mDNS service runlevel: %v", err)
		return
	}
	if enabled && missing && getAvahiDaemonPid() == "" {
		if err := mdnsRun(oomscore.Unprotected("rc-service", "nanokvm-mdns", "start")...); err != nil {
			log.Warnf("failed to start mDNS: %v", err)
		}
	}
}
