package vm

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"NanoKVM-Server/hostsensors"
	"NanoKVM-Server/internal/atomicfile"
	"NanoKVM-Server/proto"
)

// The managed computer's sensors (temperatures, voltages, fans): an agent on
// that computer, nanokvm-sensors, reads them and sends a report every few
// seconds. The agent comes with the add-on nanokvm-host-sensors; without the
// add-on nothing here runs, answers or shows (HostSensorsInstalled).
// hostSensorsFile says how the NanoKVM takes the reports:
//
//	off      not at all (absent file)
//	serial   lines on the USB serial port (/dev/ttyGS0). The server holds the
//	         port while this is set: the web terminal and the console test
//	         are refused on it, as one port carries one thing.
//	network  POST /api/host-sensors from the USB network (usb0) only: the
//	         computer has no NanoKVM credentials, and nothing on the LAN or a
//	         VPN may post readings.
var hostSensorsFile = "/etc/kvm/host-sensors"

// hostSensorsViewFile: which readings the page shows (hostsensors.View).
var hostSensorsViewFile = "/etc/kvm/host-sensors-view.json"

func hostSensorsView() hostsensors.View {
	data, err := os.ReadFile(hostSensorsViewFile)
	if err == nil {
		if view, err := hostsensors.ParseView(data); err == nil {
			return view
		} else {
			log.Errorf("host sensors view: %v", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Errorf("host sensors view: %v", err)
	}
	return hostsensors.View{Overrides: map[string]bool{}}
}

const (
	hostSensorsOff     = "off"
	hostSensorsSerial  = "serial"
	hostSensorsNetwork = "network"

	// HostSensorsIngestPath takes the agent's reports in network mode.
	HostSensorsIngestPath = "/api/host-sensors"
	hostSensorsAgentName  = "nanokvm-sensors.exe"
	// HostSensorsAgentPath is the public URL of the Windows agent.
	HostSensorsAgentPath = "/" + hostSensorsAgentName
)

var (
	// The add-on's files: the agent the computer downloads.
	hostSensorsShareDir  = "/usr/share/nanokvm/host-sensors"
	hostSensorsInterface = "usb0"
	hostSensorsDevice    = serialConsoleDevice
)

// HostSensorsInstalled: the add-on is installed (its agent is there). Read on
// every use, as the add-on inventory is: installing or removing the add-on
// needs no server restart.
func HostSensorsInstalled() bool {
	info, err := os.Stat(hostSensorsAgentFile())
	return err == nil && info.Mode().IsRegular()
}

func hostSensorsMode() string {
	data, err := os.ReadFile(hostSensorsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Errorf("host sensors setting: %v", err)
	}
	switch mode := strings.TrimSpace(string(data)); mode {
	case hostSensorsSerial, hostSensorsNetwork:
		return mode
	}
	return hostSensorsOff
}

// hostSensorsSerialReader is the goroutine that holds the serial port while
// the mode is serial.
var hostSensorsSerialReader struct {
	sync.Mutex
	stop  chan struct{}
	done  chan struct{}
	state string
}

func setHostSensorsSerialState(state string) {
	hostSensorsSerialReader.Lock()
	hostSensorsSerialReader.state = state
	hostSensorsSerialReader.Unlock()
}

// applyHostSensorsMode starts or stops the serial reader to match the mode.
// It returns after a stopped reader has closed the port and released it.
func applyHostSensorsMode(mode string) {
	r := &hostSensorsSerialReader
	r.Lock()
	stop, done := r.stop, r.done
	r.stop, r.done = nil, nil
	r.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	if mode != hostSensorsSerial {
		setHostSensorsSerialState("")
		return
	}
	stop, done = make(chan struct{}), make(chan struct{})
	r.Lock()
	r.stop, r.done, r.state = stop, done, "starting"
	r.Unlock()
	go func() {
		defer close(done)
		runHostSensorsSerial(hostSensorsDevice, stop, hostsensors.Default)
	}()
}

// hostSensorsApply serialises the changes of the reader: the setting, and
// the watch below.
var hostSensorsApply sync.Mutex

// reconcileHostSensors makes the serial reader match the add-on and the
// mode: it runs while the add-on is installed and the mode is serial.
func reconcileHostSensors() {
	hostSensorsApply.Lock()
	defer hostSensorsApply.Unlock()
	reconcileHelperDisk()
	want := HostSensorsInstalled() && hostSensorsMode() == hostSensorsSerial
	r := &hostSensorsSerialReader
	r.Lock()
	running := r.stop != nil
	r.Unlock()
	switch {
	case want && !running:
		applyHostSensorsMode(hostSensorsSerial)
	case !want && running:
		applyHostSensorsMode(hostSensorsOff)
		hostsensors.Default.Clear()
	}
}

// StartHostSensors applies the saved mode at the server's start and then
// follows the add-on: installed later, the reader starts; removed, it lets
// the serial port go.
func StartHostSensors() {
	reconcileHostSensors()
	go func() {
		for range time.Tick(10 * time.Second) {
			reconcileHostSensors()
		}
	}()
}

func hostSensorsWait(stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return false
	case <-time.After(d):
		return true
	}
}

// runHostSensorsSerial reads report lines from the gadget port until stop.
// The port comes and goes with the USB composition and the cable, and the
// terminal may hold it for a moment after the mode changed: every failure is
// retried, with the state kept for the settings page.
func runHostSensorsSerial(device string, stop <-chan struct{}, store *hostsensors.Store) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		release, ok := claimSerialPort(device)
		if !ok {
			setHostSensorsSerialState("busy")
			if !hostSensorsWait(stop, time.Second) {
				return
			}
			continue
		}
		fd, err := openSerial(&serialSettings{Device: device, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1})
		if err != nil {
			release()
			switch {
			case errors.Is(err, errSerialBusy):
				setHostSensorsSerialState("busy")
			case errors.Is(err, os.ErrNotExist), serialGone(err):
				setHostSensorsSerialState("unavailable")
			default:
				setHostSensorsSerialState("error")
				log.Warnf("host sensors: %s: %v", device, err)
			}
			if !hostSensorsWait(stop, 2*time.Second) {
				return
			}
			continue
		}
		setHostSensorsSerialState("open")
		readHostSensorLines(fd, stop, func(line []byte) {
			if err := store.Accept(line, hostSensorsSerial, time.Now()); err != nil {
				log.Debugf("host sensors: rejected a serial report: %v", err)
			}
		})
		unix.Close(fd)
		release()
		setHostSensorsSerialState("closed")
		if !hostSensorsWait(stop, time.Second) {
			return
		}
	}
}

// readHostSensorLines hands every complete line to accept until stop, a
// hang-up or a read error. A line longer than a report may be is dropped up
// to its end, so one bad sender cannot grow the buffer or hide later lines.
func readHostSensorLines(fd int, stop <-chan struct{}, accept func([]byte)) {
	buf := make([]byte, 16<<10)
	var line []byte
	overflow := false
	for {
		select {
		case <-stop:
			return
		default:
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 500)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		if n == 0 {
			continue
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			// POLLHUP or POLLERR alone: the computer closed the port or the
			// gadget was rebound.
			return
		}
		n, err = unix.Read(fd, buf)
		if n <= 0 {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		line, overflow = hostsensors.SplitLines(line, overflow, buf[:n], accept)
	}
}

// hostSensorsNetworks are the networks the agent may post from: the USB
// network's own.
func hostSensorsNetworks() []*net.IPNet {
	iface, err := net.InterfaceByName(hostSensorsInterface)
	if err != nil {
		return nil
	}
	addrs, _ := iface.Addrs()
	var nets []*net.IPNet
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && n.IP.To4() != nil {
			nets = append(nets, n)
		}
	}
	return nets
}

// PostHostSensors answers POST /api/host-sensors: one report of the agent.
// Public, because the computer has no NanoKVM credentials; taken only in
// network mode and only from the USB network. A JSON content type keeps a
// web page in the computer's browser from posting without a preflight.
func (s *Service) PostHostSensors(c *gin.Context) {
	if !HostSensorsInstalled() || hostSensorsMode() != hostSensorsNetwork {
		c.String(http.StatusForbidden, "host sensors over the network are off on this NanoKVM")
		return
	}
	if !hostsensors.FromNetwork(c.Request.RemoteAddr, hostSensorsNetworks()) {
		c.String(http.StatusForbidden, "host sensors are taken from the USB network only")
		return
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		c.String(http.StatusUnsupportedMediaType, "application/json is required")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, hostsensors.MaxReportBytes+1))
	if err != nil {
		c.String(http.StatusBadRequest, "the report could not be read")
		return
	}
	if err := hostsensors.Default.Accept(body, hostSensorsNetwork, time.Now()); err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.Status(http.StatusNoContent)
}

func hostSensorsAgentFile() string { return filepath.Join(hostSensorsShareDir, hostSensorsAgentName) }

func hostSensorsState() gin.H {
	r := &hostSensorsSerialReader
	r.Lock()
	serialState := r.state
	r.Unlock()
	addresses := []string{}
	for _, n := range hostSensorsNetworks() {
		addresses = append(addresses, n.IP.String())
	}
	// The page polls this: the agent's megabytes are not hashed here (the
	// download answers with the checksum).
	agent := proto.SerialConsoleFile{Path: HostSensorsAgentPath}
	if info, err := os.Stat(hostSensorsAgentFile()); err == nil && info.Mode().IsRegular() {
		agent.Available, agent.Size = true, info.Size()
	}
	return gin.H{
		"installed": HostSensorsInstalled(),
		"mode":      hostSensorsMode(),
		"serial":    gin.H{"port": hostSensorsDevice, "state": serialState},
		// The NanoKVM's addresses on the USB network; empty without it.
		"network": gin.H{"interface": hostSensorsInterface, "addresses": addresses, "path": HostSensorsIngestPath},
		"agent":   agent,
		"status":  hostsensors.Default.Status(time.Now()),
		"view":    hostSensorsView(),
		// The helper disk with the agent: whether the computer has it.
		"disk": hostSensorsDisk(),
	}
}

// hostSensorsDisk: the helper disk as the computer sees it. wanted: the
// mode asks for it; stale: the computer still sees the disk without the
// agent (or an older one) until the USB devices reconnect.
func hostSensorsDisk() gin.H {
	attached, stale := helperDiskLun()
	return gin.H{"attached": attached, "stale": stale, "wanted": hostSensorsMode() != hostSensorsOff}
}

// GetHostSensors answers GET /api/vm/host-sensors: the mode, the transports'
// state and the last report.
func (s *Service) GetHostSensors(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, hostSensorsState())
}

// SetHostSensors answers PUT /api/vm/host-sensors {"mode":"off|serial|network"}.
func (s *Service) SetHostSensors(c *gin.Context) {
	var rsp proto.Response
	var req struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil ||
		(req.Mode != hostSensorsOff && req.Mode != hostSensorsSerial && req.Mode != hostSensorsNetwork) {
		rsp.ErrRsp(c, -1, "mode is off, serial or network")
		return
	}
	if !HostSensorsInstalled() {
		rsp.ErrRsp(c, -3, "the host sensors add-on is not installed")
		return
	}
	if err := atomicfile.Write(hostSensorsFile, []byte(req.Mode+"\n"), 0o644); err != nil {
		log.Errorf("host sensors setting: %v", err)
		rsp.ErrRsp(c, -2, "the setting could not be saved")
		return
	}
	hostSensorsApply.Lock()
	applyHostSensorsMode(map[bool]string{true: hostSensorsSerial, false: hostSensorsOff}[req.Mode == hostSensorsSerial])
	hostSensorsApply.Unlock()
	// A report that came the other way, or before the switch-off, is not
	// what the new mode shows.
	hostsensors.Default.Clear()
	log.Infof("host sensors: %s", req.Mode)
	rsp.OkRspWithData(c, hostSensorsState())
}

// SetHostSensorsView answers PUT /api/vm/host-sensors/view {"overrides":{key:bool}}:
// which readings the page shows.
func (s *Service) SetHostSensorsView(c *gin.Context) {
	var rsp proto.Response
	if !HostSensorsInstalled() {
		rsp.ErrRsp(c, -3, "the host sensors add-on is not installed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20))
	if err != nil {
		rsp.ErrRsp(c, -1, "the view could not be read")
		return
	}
	view, err := hostsensors.ParseView(body)
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
		return
	}
	data, err := json.Marshal(view)
	if err == nil {
		err = atomicfile.Write(hostSensorsViewFile, append(data, '\n'), 0o644)
	}
	if err != nil {
		log.Errorf("host sensors view: %v", err)
		rsp.ErrRsp(c, -2, "the setting could not be saved")
		return
	}
	rsp.OkRspWithData(c, hostSensorsState())
}

// ServeHostSensorsAgent is public, like the console helper: the agent is part
// of the firmware, and the computer that fetches it has no credentials.
func (s *Service) ServeHostSensorsAgent(c *gin.Context) {
	path := hostSensorsAgentFile()
	sum, _, err := fileSha256(path)
	if err != nil {
		c.String(http.StatusNotFound, "the sensors agent is not installed on this NanoKVM")
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename=\""+hostSensorsAgentName+"\"")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Checksum-Sha256", sum)
	c.File(path)
}
