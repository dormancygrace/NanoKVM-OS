package vm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"

	"NanoKVM-Server/proto"
)

// The helper for a headless Linux host: a script that sets up a login on the
// NanoKVM's USB serial port (CDC ACM) on the computer it is plugged into. It
// is served at a public URL for curl/wget over the LAN or the USB network,
// and S03usbdev attaches it as a small read-only FAT disk (a second LUN of
// the virtual drive) whenever USB serial and the virtual disk are enabled,
// so the host needs no network at all. The script documents what it does.
const (
	serialConsoleScriptName = "nanokvm-serial-console.sh"
	serialConsoleDiskName   = "nanokvm-serial-console.img"
	// SerialConsoleScriptPath is the public URL of the script.
	SerialConsoleScriptPath = "/" + serialConsoleScriptName
	serialConsoleDevice     = "/dev/ttyGS0"
	serialConsoleTimeout    = 5 * time.Second
)

var (
	serialConsoleShareDir  = "/kvmapp/system/share/serial-console"
	gadgetSerialNumberPath = "/sys/kernel/config/usb_gadget/g0/strings/0x409/serialnumber"
	// The helper LUN's backing file, as S03usbdev sets it.
	serialConsoleLunFile = "/sys/kernel/config/usb_gadget/g0/functions/mass_storage.disk0/lun.1/file"
	serialConsoleProbe   = probeSerialConsole
)

func serialConsoleScriptFile() string {
	return filepath.Join(serialConsoleShareDir, serialConsoleScriptName)
}
func serialConsoleDiskFile() string {
	return filepath.Join(serialConsoleShareDir, serialConsoleDiskName)
}

func fileSha256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func gadgetSerialNumber() string {
	data, err := os.ReadFile(gadgetSerialNumberPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func serialConsoleState() *proto.SerialConsoleRsp {
	rsp := &proto.SerialConsoleRsp{SerialNumber: gadgetSerialNumber(), Port: serialConsoleDevice,
		Script: proto.SerialConsoleFile{Path: SerialConsoleScriptPath},
		Disk:   proto.SerialConsoleDisk{File: serialConsoleDiskFile()}}
	if sum, size, err := fileSha256(serialConsoleScriptFile()); err == nil {
		rsp.Script.Available, rsp.Script.Sha256, rsp.Script.Size = true, sum, size
	}
	if _, err := os.Stat(serialConsoleDiskFile()); err == nil {
		rsp.Disk.Available = true
		if data, err := os.ReadFile(serialConsoleLunFile); err == nil {
			lun := strings.TrimSpace(string(data))
			// A package upgrade replaces the image while the LUN holds the
			// old one; the kernel then shows the path as deleted. The host
			// sees the old image until the USB devices reconnect.
			stale := strings.HasSuffix(lun, " (deleted)")
			if strings.TrimSuffix(lun, " (deleted)") == serialConsoleDiskFile() {
				rsp.Disk.Attached, rsp.Disk.Stale = true, stale
			}
		}
	}
	return rsp
}

func (s *Service) GetSerialConsole(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, serialConsoleState())
}

// ServeSerialConsoleScript is public: the script is part of the firmware
// source, and the host that fetches it has no NanoKVM credentials.
func (s *Service) ServeSerialConsoleScript(c *gin.Context) {
	path := serialConsoleScriptFile()
	sum, _, err := fileSha256(path)
	if err != nil {
		c.String(http.StatusNotFound, "the serial console helper is not installed on this NanoKVM")
		return
	}
	c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	c.Header("Content-Disposition", "inline; filename=\""+serialConsoleScriptName+"\"")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Checksum-Sha256", sum)
	c.File(path)
}

func (s *Service) TestSerialConsole(c *gin.Context) {
	var rsp proto.Response
	result := serialConsoleProbe(serialConsoleDevice, serialConsoleTimeout)
	if result.Err != nil {
		log.Warnf("serial console test: %s", result.Err)
	}
	rsp.OkRspWithData(c, proto.SerialConsoleTestRsp{
		Ok:    result.State == "login" || result.State == "password" || result.State == "shell",
		State: result.State, Response: result.Response})
}

// probeResult is what the host answered to a carriage return on the port.
// State: login (a login prompt), password, shell (an open session), text
// (something else), silent (nothing within the timeout), busy (the web
// terminal has the port open), unavailable (no USB serial), error.
type probeResult struct {
	State    string
	Response string
	Err      error
}

// picocom (the web terminal) holds a UUCP lock while it has the port open.
var serialLockDir = "/var/lock"

func serialLockFile(device string) string {
	return filepath.Join(serialLockDir, "LCK.."+filepath.Base(device))
}

// deviceInUse reports whether another process has the device open: the
// terminal session, or anything else that would compete for the bytes.
func deviceInUse(device string) bool {
	self := strconv.Itoa(os.Getpid())
	procs, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range procs {
		if strings.Split(fd, "/")[2] == self {
			continue
		}
		if target, err := os.Readlink(fd); err == nil && target == device {
			return true
		}
	}
	return false
}

// probeSerialConsole sends one carriage return on the gadget port and reads
// what the host sends back, as the browser terminal's first Enter would.
func probeSerialConsole(device string, timeout time.Duration) probeResult {
	if _, err := os.Stat(serialLockFile(device)); err == nil || deviceInUse(device) {
		return probeResult{State: "busy"}
	}
	f, err := os.OpenFile(device, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENXIO) || errors.Is(err, unix.ENODEV) {
			return probeResult{State: "unavailable", Err: err}
		}
		return probeResult{State: "error", Err: err}
	}
	defer f.Close()
	// f.Fd() makes the descriptor blocking again; the reads below return at
	// once only because of VMIN=0/VTIME=0, so without termios there is no
	// drain and no read loop, only the write.
	fd := int(f.Fd())
	rawMode := false
	if saved, err := unix.IoctlGetTermios(fd, unix.TCGETS); err == nil {
		raw := *saved
		raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
		raw.Oflag &^= unix.OPOST
		raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
		raw.Cflag &^= unix.CSIZE | unix.PARENB
		raw.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
		raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 0, 0
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err == nil {
			defer unix.IoctlSetTermios(fd, unix.TCSETS, saved)
			rawMode = true
		}
	}
	if !rawMode {
		return probeResult{State: "error", Err: errors.New("not a terminal device")}
	}
	// Whatever the host sent before (a banner, a previous prompt) is not the
	// answer to this Enter.
	drain := make([]byte, 4096)
	for {
		n, err := unix.Read(fd, drain)
		if n <= 0 || err != nil {
			break
		}
	}
	if _, err := unix.Write(fd, []byte("\r")); err != nil {
		return probeResult{State: "error", Err: err}
	}
	var data []byte
	deadline := time.Now().Add(timeout)
	quietSince := time.Time{}
	for time.Now().Before(deadline) && len(data) < 4096 {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, _ := unix.Poll(fds, 100); n > 0 {
			buf := make([]byte, 1024)
			n, err := unix.Read(fd, buf)
			if n > 0 {
				data = append(data, buf[:n]...)
				quietSince = time.Now()
				switch state, _ := classifySerialResponse(data); state {
				case "login", "password":
					return finishProbe(data)
				}
				continue
			}
			if err != nil && !errors.Is(err, unix.EAGAIN) {
				break
			}
		}
		// A prompt is complete once the host has been quiet for a moment.
		if len(data) > 0 && time.Since(quietSince) > 400*time.Millisecond {
			break
		}
	}
	return finishProbe(data)
}

func finishProbe(data []byte) probeResult {
	state, text := classifySerialResponse(data)
	return probeResult{State: state, Response: text}
}

var (
	ansiCSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	ansiOSC = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)
	spaces  = regexp.MustCompile(`[ \t]+`)
)

// classifySerialResponse reads the host's answer: a login prompt, a password
// prompt, an open shell (its prompt ends the text), other text, or nothing.
func classifySerialResponse(data []byte) (string, string) {
	clean := ansiOSC.ReplaceAll(ansiCSI.ReplaceAll(data, nil), nil)
	clean = bytes.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, clean)
	text := strings.TrimSpace(spaces.ReplaceAllString(string(clean), " "))
	if len(text) > 200 {
		text = text[len(text)-200:]
	}
	lower := strings.ToLower(text)
	switch {
	case text == "":
		return "silent", ""
	case strings.Contains(lower, "login:"):
		return "login", text
	case strings.Contains(lower, "password:"):
		return "password", text
	case strings.HasSuffix(text, "$") || strings.HasSuffix(text, "#") || strings.HasSuffix(text, ">") || strings.HasSuffix(text, "%"):
		return "shell", text
	}
	return "text", text
}
