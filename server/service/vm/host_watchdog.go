package vm

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

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
	signs.Watching = config.Action != hostwatchdog.Off && hostSensorsMode() == hostSensorsNetwork
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
// whatever its firewall; a hung one does not.
//
// The server sends the requests itself (arpProbe) instead of running
// arping: BusyBox's ends with "no reply" also when not one request could be
// sent, and a question that was not asked must not pass for a host that
// did not answer. why says what failed.
var askHost = func(peer string) (answer, why string) {
	target := net.ParseIP(peer)
	if target == nil {
		return hostUnknown, "the address of the last report is not known"
	}
	if carrier, err := os.ReadFile("/sys/class/net/" + hostSensorsInterface + "/carrier"); err != nil || strings.TrimSpace(string(carrier)) != "1" {
		return hostUnknown, "the USB network is down on the NanoKVM"
	}
	sent, answered, err := arpProbe(hostSensorsInterface, target, arpRequests, arpWait)
	return arpVerdict(sent, answered, err)
}

const (
	// Requests a second apart, and how long answers are waited for in all.
	arpRequests = 3
	arpWait     = 5 * time.Second
)

// arpVerdict: the host is silent only when every request went out and none
// was answered.
func arpVerdict(sent int, answered bool, err error) (answer, why string) {
	switch {
	case answered:
		return hostAlive, ""
	case err != nil:
		return hostUnknown, "ARP on the USB network: " + err.Error()
	case sent < arpRequests:
		return hostUnknown, "ARP on the USB network: only " + strconv.Itoa(sent) + " of " + strconv.Itoa(arpRequests) + " requests were sent"
	}
	return hostSilent, ""
}

// arpProbe sends count ARP requests for target on the named interface, a
// second apart, and listens for wait in all. It reports how many requests
// the kernel took, whether target answered (any ARP packet with target as
// its sender counts), and the first error; it stops at a failed send.
func arpProbe(name string, target net.IP, count int, wait time.Duration) (sent int, answered bool, err error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, false, err
	}
	target = target.To4()
	var source net.IP
	addrs, _ := iface.Addrs()
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && n.IP.To4() != nil && target != nil && n.Contains(target) {
			source = n.IP.To4()
		}
	}
	if source == nil || len(iface.HardwareAddr) != 6 {
		return 0, false, errors.New("the address is not on this interface's network")
	}
	protocol := uint16(unix.ETH_P_ARP<<8&0xff00 | unix.ETH_P_ARP>>8) // network byte order
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, int(protocol))
	if err != nil {
		return 0, false, err
	}
	defer unix.Close(fd)
	if err = unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: protocol, Ifindex: iface.Index}); err != nil {
		return 0, false, err
	}
	// An Ethernet/IPv4 ARP request; the kernel adds the link header.
	request := make([]byte, 28)
	binary.BigEndian.PutUint16(request[0:], 1)      // Ethernet
	binary.BigEndian.PutUint16(request[2:], 0x0800) // IPv4
	request[4], request[5] = 6, 4
	binary.BigEndian.PutUint16(request[6:], 1) // request
	copy(request[8:14], iface.HardwareAddr)
	copy(request[14:18], source)
	copy(request[24:28], target)
	everyone := &unix.SockaddrLinklayer{Protocol: protocol, Ifindex: iface.Index, Hatype: 1, Halen: 6,
		Addr: [8]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	deadline := time.Now().Add(wait)
	next := time.Now()
	packet := make([]byte, 128)
	for time.Now().Before(deadline) {
		if sent < count && !time.Now().Before(next) {
			if err := unix.Sendto(fd, request, 0, everyone); err != nil {
				return sent, false, fmt.Errorf("sending a request: %w", err)
			}
			sent++
			next = next.Add(time.Second)
		}
		until := deadline
		if sent < count && next.Before(until) {
			until = next
		}
		ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, int(time.Until(until)/time.Millisecond)+1)
		if err != nil && err != unix.EINTR {
			return sent, false, err
		}
		if ready <= 0 {
			continue
		}
		n, _, err := unix.Recvfrom(fd, packet, unix.MSG_DONTWAIT)
		if err == nil && n >= 28 && bytes.Equal(packet[14:18], target) {
			return sent, true, nil
		}
	}
	return sent, false, nil
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
	// decision: the setting (it was made under the action and the time of
	// its moment: another action must not inherit it, least of all a
	// press after "notify"), a report, a restart of the host, sleep, power
	// off.
	now = time.Now()
	decidedUnder := config
	config = hostwatchdog.Load(hostWatchdogFile)
	signs = hostWatchdogSigns(config, now)
	hostWatchdog.Lock()
	still := config == decidedUnder && hostWatchdog.state.StillDue(signs, time.Duration(config.Minutes)*time.Minute)
	if !still {
		// Nothing was done: the next step decides anew, under the setting
		// and with the signs of then, and asks the host again.
		hostWatchdog.state.Reopen()
	}
	hostWatchdog.Unlock()
	if !still {
		log.Infof("host watchdog: the host did not answer, but the situation changed while it was asked: nothing done, deciding anew")
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
