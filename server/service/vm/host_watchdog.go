package vm

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/hostsensors"
	"NanoKVM-Server/hostwatchdog"
	"NanoKVM-Server/journal"
	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/notify"
)

// The host watchdog (package hostwatchdog): the server gathers the signs,
// asks the host when the watchdog is due, and acts.

var hostWatchdogFile = "/etc/kvm/host-watchdog.json"

const hostWatchdogStep = 5 * time.Second

// HostWatchdogPower presses for the watchdog: the BMC power actions, as
// Redfish and IPMI use them. A variable for the tests that must not press.
var HostWatchdogPower interface {
	Reset(context.Context) error
	Cycle(context.Context) error
} = &bmc.Power{ATX: ATX{}}

var hostWatchdog struct {
	sync.Mutex
	state hostwatchdog.Watchdog
	// last is what the watchdog last decided, for the page.
	last string
}

// hostWatchdogSigns reads what the NanoKVM sees of the host now.
func hostWatchdogSigns(config hostwatchdog.Config, now time.Time) hostwatchdog.Signs {
	signs := hostwatchdog.Signs{Now: now}
	signs.Watching = config.Action != hostwatchdog.Off && HostSensorsInstalled() && hostSensorsMode() == hostSensorsNetwork
	if !signs.Watching {
		return signs
	}
	signs.Report, _ = hostsensors.Default.Last()
	signs.PowerOn, signs.PowerKnown = notifyPowerLED()
	// The USB network's carrier goes with every new enumeration.
	changes, _ := os.ReadFile("/sys/class/net/" + hostSensorsInterface + "/carrier_changes")
	signs.Link = strings.TrimSpace(string(changes))
	udc, _ := os.ReadFile(usbGadgetUDCState())
	signs.Configured = strings.TrimSpace(string(udc)) == "configured"
	return signs
}

// usbGadgetUDCState is the state file of the gadget's USB controller.
func usbGadgetUDCState() string {
	name, _ := os.ReadFile("/sys/kernel/config/usb_gadget/g0/UDC")
	return "/sys/class/udc/" + strings.TrimSpace(string(name)) + "/state"
}

// hostAnswers asks the host for a sign of life: ARP requests on the USB
// network to the address its reports came from. The host's kernel answers
// them whatever its firewall; a hung one does not.
var hostAnswers = func(peer string) bool {
	if peer == "" {
		return true // nobody to ask: do not act on a guess
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "arping", "-q", "-c", "3", "-w", "5", "-I", hostSensorsInterface, peer).Run() == nil
}

func runHostWatchdog() {
	for range time.Tick(hostWatchdogStep) {
		stepHostWatchdog(time.Now())
	}
}

func stepHostWatchdog(now time.Time) {
	config := hostwatchdog.Load(hostWatchdogFile)
	signs := hostWatchdogSigns(config, now)
	hostWatchdog.Lock()
	due := hostWatchdog.state.Step(signs, time.Duration(config.Minutes)*time.Minute)
	if due {
		hostWatchdog.state.Decided()
	}
	hostWatchdog.Unlock()
	if !due {
		return
	}
	// Asking the host and pressing take seconds: the page is not kept
	// waiting for them.
	_, peer := hostsensors.Default.Last()
	vars := map[string]string{"minutes": strconv.Itoa(config.Minutes)}
	emit := func(severity notify.Severity, message string) {
		hostWatchdog.Lock()
		hostWatchdog.last = message
		hostWatchdog.Unlock()
		notify.Emit(notify.Event{Kind: notify.KindPower, Severity: severity, Message: message, Vars: vars})
	}
	if hostAnswers(peer) {
		// The agent stopped; the host lives.
		emit(notify.SeverityInfo, "watchdog.agent-silent")
		return
	}
	ctx, cancel := context.WithTimeout(journal.WithActor(context.Background(), "watchdog"), time.Minute)
	defer cancel()
	var err error
	message := "watchdog.host-silent"
	switch config.Action {
	case hostwatchdog.Reset:
		message, err = "watchdog.reset", HostWatchdogPower.Reset(ctx)
	case hostwatchdog.Cycle:
		message, err = "watchdog.cycle", HostWatchdogPower.Cycle(ctx)
	}
	if err != nil {
		log.Errorf("host watchdog: %v", err)
		vars["error"] = err.Error()
		message = "watchdog.failed"
	}
	emit(notify.SeverityCritical, message)
}

func hostWatchdogState() gin.H {
	config := hostwatchdog.Load(hostWatchdogFile)
	hostWatchdog.Lock()
	defer hostWatchdog.Unlock()
	return gin.H{"action": config.Action, "minutes": config.Minutes, "armed": hostWatchdog.state.Armed(), "last": hostWatchdog.last}
}

// SetHostWatchdog answers PUT /api/vm/host-sensors/watchdog
// {"action":"off|notify|reset|cycle","minutes":2..60}.
func (s *Service) SetHostWatchdog(c *gin.Context) {
	var rsp proto.Response
	var config hostwatchdog.Config
	if err := c.ShouldBindJSON(&config); err != nil {
		rsp.ErrRsp(c, -1, "invalid watchdog setting")
		return
	}
	if err := hostwatchdog.Save(hostWatchdogFile, config); err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	log.Infof("host watchdog: %s after %d min", config.Action, config.Minutes)
	rsp.OkRspWithData(c, hostSensorsState())
}
