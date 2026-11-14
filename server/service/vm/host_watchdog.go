package vm

import (
	"context"
	"errors"
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
	var from hostsensors.From
	signs.Report, from = hostsensors.Default.Last()
	signs.ReportLink = from.Link
	signs.PowerOn, signs.PowerKnown = notifyPowerLED()
	signs.Link = hostWatchdogLink()
	udc, _ := os.ReadFile(usbGadgetUDCState())
	signs.Configured = strings.TrimSpace(string(udc)) == "configured"
	return signs
}

// hostWatchdogLink names the life of the USB link: the USB network's
// carrier goes with every new enumeration, and the kernel counts its
// changes. "" when it cannot be read: nothing is decided then.
func hostWatchdogLink() string {
	changes, err := os.ReadFile("/sys/class/net/" + hostSensorsInterface + "/carrier_changes")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(changes))
}

// usbGadgetUDCState is the state file of the gadget's USB controller.
func usbGadgetUDCState() string {
	name, _ := os.ReadFile("/sys/kernel/config/usb_gadget/g0/UDC")
	return "/sys/class/udc/" + strings.TrimSpace(string(name)) + "/state"
}

// What asking the host came to. Only hostSilent is evidence of a hang.
const (
	hostAlive   = "alive"
	hostSilent  = "silent"
	hostUnknown = "unknown" // the question could not be asked
)

// askHost asks the host for a sign of life: ARP requests on the USB network
// to the address its reports came from. The host's kernel answers them
// whatever its firewall; a hung one does not. The answer is hostSilent only
// when the requests went out and none was answered: arping (BusyBox) ends
// with 0 for a reply, 1 for none, 2 for an error; a command that could not
// be started, was killed, or ran out of time proves nothing, and neither
// does an interface that is down. why says what failed.
var askHost = func(peer string) (answer, why string) {
	if peer == "" {
		return hostUnknown, "the address of the last report is not known"
	}
	if carrier, err := os.ReadFile("/sys/class/net/" + hostSensorsInterface + "/carrier"); err != nil || strings.TrimSpace(string(carrier)) != "1" {
		return hostUnknown, "the USB network is down on the NanoKVM"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "arping", "-q", "-c", "3", "-w", "5", "-I", hostSensorsInterface, peer).Run()
	return arpProbeResult(err, ctx.Err())
}

func arpProbeResult(err, timeout error) (answer, why string) {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return hostAlive, ""
	case timeout == nil && errors.As(err, &exit) && exit.ExitCode() == 1:
		return hostSilent, ""
	}
	return hostUnknown, "arping: " + err.Error()
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
	_, from := hostsensors.Default.Last()
	vars := map[string]string{"minutes": strconv.Itoa(config.Minutes)}
	emit := func(severity notify.Severity, message string) {
		hostWatchdog.Lock()
		hostWatchdog.last = message
		hostWatchdog.Unlock()
		notify.Emit(notify.Event{Kind: notify.KindPower, Severity: severity, Message: message, Vars: vars})
	}
	switch answer, why := askHost(from.Peer); answer {
	case hostAlive:
		// The agent stopped; the host lives.
		emit(notify.SeverityInfo, "watchdog.agent-silent")
		return
	case hostUnknown:
		// Not asked is not "did not answer": nothing is pressed.
		log.Warnf("host watchdog: the host could not be asked: %s", why)
		vars["error"] = why
		emit(notify.SeverityWarning, "watchdog.unknown")
		return
	}
	// The question took seconds. Whatever changed meanwhile ends the
	// decision: the watchdog switched off or set otherwise, a report, a
	// restart of the host, sleep, power off.
	now = time.Now()
	config = hostwatchdog.Load(hostWatchdogFile)
	signs = hostWatchdogSigns(config, now)
	hostWatchdog.Lock()
	still := hostWatchdog.state.StillDue(signs)
	hostWatchdog.Unlock()
	if !still {
		log.Infof("host watchdog: the host did not answer, but the situation changed while it was asked: nothing done")
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
