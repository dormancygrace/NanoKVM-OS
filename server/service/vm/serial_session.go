package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

// The serial terminal: the browser's bytes go to a tty device and back,
// without a PTY or an external program. The device is opened once per
// session with exclusive access; the probe in serial_console.go sees it.

var terminalPortPattern = regexp.MustCompile(`^/dev/(tty(S|USB|ACM|GS)[0-9]+|serial[0-9]+)$`)

type serialSettings struct {
	Device   string
	Baud     int
	Parity   string // none, even, odd
	Flow     string // none, hard, soft
	DataBits int
	StopBits int
}

var serialBauds = map[int]uint32{
	50: unix.B50, 75: unix.B75, 110: unix.B110, 134: unix.B134, 150: unix.B150, 200: unix.B200,
	300: unix.B300, 600: unix.B600, 1200: unix.B1200, 1800: unix.B1800, 2400: unix.B2400,
	4800: unix.B4800, 9600: unix.B9600, 19200: unix.B19200, 38400: unix.B38400, 57600: unix.B57600,
	115200: unix.B115200, 230400: unix.B230400, 460800: unix.B460800, 500000: unix.B500000,
	576000: unix.B576000, 921600: unix.B921600, 1000000: unix.B1000000, 1152000: unix.B1152000,
	1500000: unix.B1500000, 2000000: unix.B2000000, 2500000: unix.B2500000, 3000000: unix.B3000000,
	3500000: unix.B3500000, 4000000: unix.B4000000,
}

// serialSettingsFromQuery validates the terminal's serial parameters. The
// gadget port (ttyGS) is a USB byte stream whose line coding is nominal: it
// always gets 9600 8N1 without flow control, so inherited termios cannot
// swallow stream bytes.
func serialSettingsFromQuery(q url.Values) (*serialSettings, error) {
	port := strings.TrimSpace(q.Get("port"))
	if !terminalPortPattern.MatchString(port) {
		return nil, fmt.Errorf("invalid serial port")
	}
	s := &serialSettings{Device: port, Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1}
	if strings.HasPrefix(port, "/dev/ttyGS") {
		return s, nil
	}
	option := func(key, fallback string) (string, error) {
		value := strings.ToLower(strings.TrimSpace(q.Get(key)))
		if value == "" {
			value = fallback
		}
		if strings.ContainsAny(value, " \t\r\n") {
			return "", fmt.Errorf("invalid serial %s", key)
		}
		return value, nil
	}
	baud, err := option("baud", "115200")
	if err != nil {
		return nil, err
	}
	if s.Baud, err = strconv.Atoi(baud); err != nil || serialBauds[s.Baud] == 0 {
		return nil, fmt.Errorf("invalid serial baud")
	}
	parity, err := option("parity", "none")
	if err != nil {
		return nil, err
	}
	switch parity {
	case "n", "none":
		s.Parity = "none"
	case "e", "even":
		s.Parity = "even"
	case "o", "odd":
		s.Parity = "odd"
	default:
		return nil, fmt.Errorf("invalid serial parity")
	}
	flow, err := option("flowControl", "none")
	if err != nil {
		return nil, err
	}
	switch flow {
	case "n", "none":
		s.Flow = "none"
	case "h", "hard":
		s.Flow = "hard"
	case "s", "x", "soft":
		s.Flow = "soft"
	default:
		return nil, fmt.Errorf("invalid serial flowControl")
	}
	bits, err := option("dataBits", "8")
	if err != nil {
		return nil, err
	}
	if s.DataBits, err = strconv.Atoi(bits); err != nil || s.DataBits < 5 || s.DataBits > 8 {
		return nil, fmt.Errorf("invalid serial dataBits")
	}
	stop, err := option("stopBits", "1")
	if err != nil {
		return nil, err
	}
	if s.StopBits, err = strconv.Atoi(stop); err != nil || (s.StopBits != 1 && s.StopBits != 2) {
		return nil, fmt.Errorf("invalid serial stopBits")
	}
	return s, nil
}

// termios returns the raw line settings for the device.
func (s *serialSettings) termios(base *unix.Termios) *unix.Termios {
	t := *base
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF | unix.IXANY
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.PARODD | unix.CSTOPB | unix.CRTSCTS | unix.CBAUD
	t.Cflag |= unix.CREAD | unix.CLOCAL
	switch s.DataBits {
	case 5:
		t.Cflag |= unix.CS5
	case 6:
		t.Cflag |= unix.CS6
	case 7:
		t.Cflag |= unix.CS7
	default:
		t.Cflag |= unix.CS8
	}
	switch s.Parity {
	case "even":
		t.Cflag |= unix.PARENB
	case "odd":
		t.Cflag |= unix.PARENB | unix.PARODD
	}
	if s.StopBits == 2 {
		t.Cflag |= unix.CSTOPB
	}
	switch s.Flow {
	case "hard":
		t.Cflag |= unix.CRTSCTS
	case "soft":
		t.Iflag |= unix.IXON | unix.IXOFF
	}
	speed := serialBauds[s.Baud]
	if speed == 0 {
		speed = unix.B9600
	}
	t.Cflag |= speed
	t.Ispeed, t.Ospeed = speed, speed
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 0, 0
	return &t
}

var errSerialBusy = errors.New("serial port is in use")

// openSerial opens the device with exclusive access and the line settings.
func openSerial(s *serialSettings) (int, error) {
	fd, err := unix.Open(s.Device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	// flock against another server session, TIOCEXCL against other programs.
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return -1, errSerialBusy
	}
	_ = unix.IoctlSetInt(fd, unix.TIOCEXCL, 0)
	base, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("not a serial device: %w", err)
	}
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, s.termios(base)); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("line settings: %w", err)
	}
	_ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIOFLUSH)
	return fd, nil
}

func serialGone(err error) bool {
	return errors.Is(err, unix.EIO) || errors.Is(err, unix.ENXIO) || errors.Is(err, unix.ENODEV)
}

var errSessionEnded = errors.New("serial session ended")

// writeAllSerial writes data to the non-blocking fd, waiting for room in
// short slices so the wait ends with the session: a line that does not take
// data (flow control asserted, nothing at the other end) must not keep the
// device open after the browser has gone.
func writeAllSerial(fd int, data []byte, stop <-chan struct{}) error {
	for len(data) > 0 {
		select {
		case <-stop:
			return errSessionEnded
		default:
		}
		n, err := unix.Write(fd, data)
		if n > 0 {
			data = data[n:]
			continue
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if _, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}, 200); err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
	}
	return nil
}

// wsWriter serialises the data writes to the WebSocket: the device side
// and the input side both send bytes and notices, and gorilla allows one
// writer at a time. Control frames (the pings) are safe alongside and stay
// outside.
type wsWriter struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (w *wsWriter) write(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.ws.SetWriteDeadline(time.Now().Add(messageWait))
	return w.ws.WriteMessage(websocket.BinaryMessage, data)
}

func (w *wsWriter) notice(text string) {
	_ = w.write([]byte("\r\n\x1b[31m" + text + "\x1b[0m\r\n"))
}

const serialDisconnectedNotice = "The serial device was disconnected."

// serialPingInterval is how often the session pings the browser. A ping to
// a browser that has gone fails, which ends the session even while the
// input side is waiting on the device; tests shorten it.
var serialPingInterval = 10 * time.Second

// serialInputQueue is how many browser messages may wait for the device
// (each at most maxReadSize): the WebSocket reader never blocks on the tty,
// so it keeps seeing the browser; beyond that it waits for the session's end.
const serialInputQueue = 32

// runSerialSession bridges the WebSocket and the serial device until either
// side goes away. Resize messages (JSON) have no meaning for a serial line
// and are dropped; the browser's OSC 777 size reply is ordinary input.
func runSerialSession(ws *websocket.Conn, s *serialSettings) error {
	return runSerialSessionWithPoll(ws, s, unix.Poll)
}

// Inject the device-read poll so interrupted waits can be tested deterministically.
func runSerialSessionWithPoll(ws *websocket.Conn, s *serialSettings, poll func([]unix.PollFd, int) (int, error)) error {
	out := &wsWriter{ws: ws}
	release, ok := claimSerialPort(s.Device)
	if !ok {
		out.notice("The serial port is in use by another session (a terminal or the console test).")
		return nil
	}
	defer release()
	fd, err := openSerial(s)
	if err != nil {
		switch {
		case errors.Is(err, errSerialBusy):
			out.notice("The serial port is in use by another program.")
		case errors.Is(err, os.ErrNotExist), serialGone(err):
			out.notice("The serial port is not available.")
		default:
			out.notice("The serial port could not be opened.")
		}
		return err
	}
	// Closed after every goroutine that uses it has returned, and released
	// only then: a new session must not open the device while an old
	// goroutine still polls a reused descriptor number.
	var wg sync.WaitGroup
	defer unix.Close(fd)
	defer wg.Wait()

	_ = ws.SetReadDeadline(time.Now().Add(terminalPongWait))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(terminalPongWait)) })
	stop := make(chan struct{})
	var stopOnce sync.Once
	finish := func() { stopOnce.Do(func() { close(stop) }) }
	defer finish()

	// Pings, and with them the watch on the connection's life.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(serialPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(messageWait)) != nil {
					finish()
					_ = ws.Close()
					return
				}
			}
		}
	}()

	// Device to browser.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ws.Close()
		defer finish()
		buf := make([]byte, maxMessageSize)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 200)
			if err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				return
			}
			if n == 0 {
				continue
			}
			n, err = unix.Read(fd, buf)
			if n > 0 {
				if out.write(buf[:n]) != nil {
					return
				}
				continue
			}
			if err == nil || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				if err == nil {
					// end of file on a tty: the other side hung up
					out.notice(serialDisconnectedNotice)
					return
				}
				continue
			}
			if serialGone(err) {
				out.notice(serialDisconnectedNotice)
			}
			return
		}
	}()

	// Browser to device, from a queue: the WebSocket reader below never
	// waits on the tty itself, so a browser that goes is seen as it goes.
	in := make(chan []byte, serialInputQueue)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer ws.Close()
		defer finish()
		for {
			select {
			case <-stop:
				return
			case data := <-in:
				if err := writeAllSerial(fd, data, stop); err != nil {
					if serialGone(err) {
						out.notice(serialDisconnectedNotice)
					}
					return
				}
			}
		}
	}()

	ws.SetReadLimit(maxReadSize)
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if kind == websocket.BinaryMessage {
			var size WinSize
			if json.Unmarshal(data, &size) == nil {
				continue
			}
		}
		select {
		case in <- data:
		case <-stop:
		}
	}
	finish()
	_ = ws.Close()
	return nil
}
