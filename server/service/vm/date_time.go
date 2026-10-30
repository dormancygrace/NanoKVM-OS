package vm

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/timeconfig"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

var deviceTime = timeconfig.Store{Dir: "/etc", Restart: func() error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	argv := oomscore.Unprotected("/etc/init.d/S49chronyd", "restart")
	return exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
}}

func (s *Service) GetDateTime(c *gin.Context) {
	var rsp proto.Response
	cfg, err := deviceTime.Read()
	if err != nil {
		rsp.ErrRsp(c, -1, "Cannot read date and time settings")
		return
	}
	synced, err := clockSynchronized()
	rsp.OkRspWithData(c, gin.H{"config": cfg, "zones": timeconfig.Zones(), "now": time.Now().UnixMilli(),
		"synchronized": err == nil && synced, "daemon": "chrony"})
}

func (s *Service) SetDateTime(c *gin.Context) {
	var rsp proto.Response
	var cfg timeconfig.Config
	if err := c.ShouldBindJSON(&cfg); err != nil {
		rsp.ErrRsp(c, -1, "Invalid date and time settings")
		return
	}
	if err := timeconfig.Validate(cfg); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	if err := deviceTime.Save(cfg); err != nil {
		rsp.ErrRsp(c, -2, "Cannot apply date and time settings; previous configuration was restored where possible")
		return
	}
	s.GetDateTime(c)
}

// The clock a browser may set: later than this OS can be built, earlier than
// any RTC it runs with keeps counting.
var (
	clockMin = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clockMax = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// For tests: the NTP state, the system clock and the RTC.
var (
	clockSynchronized = timeconfig.ChronySynchronized
	setSystemClock    = func(t time.Time) error {
		tv := syscall.NsecToTimeval(t.UnixNano())
		return syscall.Settimeofday(&tv)
	}
	saveClockToRTC = func() error {
		if _, err := os.Stat("/dev/rtc0"); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, "hwclock", "-w", "-u").Run()
	}
)

// SetClock sets the clock to the administrator's browser time, for networks
// without NTP. A clock that chrony keeps synchronised is left to it. Shutdown
// saves only a synchronised clock to the RTC (nanokvm-storage), so the time
// set here goes to the RTC at once and survives a reboot.
func (s *Service) SetClock(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Now int64 `json:"now"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "Invalid time")
		return
	}
	now := time.UnixMilli(req.Now)
	if now.Before(clockMin) || !now.Before(clockMax) {
		rsp.ErrRsp(c, -1, "Invalid time")
		return
	}
	if synced, err := clockSynchronized(); err == nil && synced {
		rsp.ErrRsp(c, -1, "The clock is synchronised by NTP")
		return
	}
	if err := setSystemClock(now); err != nil {
		log.Errorf("set the clock: %v", err)
		rsp.ErrRsp(c, -2, "Cannot set the clock")
		return
	}
	if err := saveClockToRTC(); err != nil {
		log.Warnf("save the clock to the RTC: %v", err)
		rsp.ErrRsp(c, -2, "System clock set, but could not save it to the RTC")
		return
	}
	s.GetDateTime(c)
}
