package vm

import (
	"net/url"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSerialSettingsFromQuery(t *testing.T) {
	// the gadget port: nominal 9600 8N1, options ignored
	for _, query := range []string{"port=/dev/ttyGS0", "port=/dev/ttyGS0&baud=9600", "port=/dev/ttyGS0&baud=115200&parity=even"} {
		q, _ := url.ParseQuery(query)
		s, err := serialSettingsFromQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		if *s != (serialSettings{Device: "/dev/ttyGS0", Baud: 9600, Parity: "none", Flow: "none", DataBits: 8, StopBits: 1}) {
			t.Fatalf("gadget settings: %+v", s)
		}
	}
	// a UART keeps its settings
	q, _ := url.ParseQuery("port=/dev/ttyS1&baud=9600&parity=even&flowControl=hard&dataBits=7&stopBits=2")
	s, err := serialSettingsFromQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	if *s != (serialSettings{Device: "/dev/ttyS1", Baud: 9600, Parity: "even", Flow: "hard", DataBits: 7, StopBits: 2}) {
		t.Fatalf("UART settings lost: %+v", s)
	}
	q, _ = url.ParseQuery("port=/dev/ttyUSB0")
	if s, err = serialSettingsFromQuery(q); err != nil || s.Baud != 115200 || s.Parity != "none" || s.DataBits != 8 {
		t.Fatalf("UART defaults: %+v %v", s, err)
	}
	for _, query := range []string{"port=/etc/passwd", "port=/dev/ttyS1%3Bid", "port=/dev/ttyS1&baud=12345", "port=/dev/ttyS1&parity=none%20even",
		"port=", "port=/dev/ttyS1&dataBits=9", "port=/dev/ttyS1&stopBits=3", "port=/dev/ttyS1&flowControl=yes"} {
		q, _ := url.ParseQuery(query)
		if _, err := serialSettingsFromQuery(q); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	if terminalCommand().Path != "/bin/sh" {
		t.Fatal("shell terminal changed")
	}
}

func TestSerialTermios(t *testing.T) {
	base := &unix.Termios{Iflag: unix.ICRNL | unix.IXON, Oflag: unix.OPOST, Lflag: unix.ECHO | unix.ICANON, Cflag: unix.CS7 | unix.B300 | unix.HUPCL}
	raw := (&serialSettings{Baud: 115200, Parity: "even", Flow: "hard", DataBits: 8, StopBits: 2}).termios(base)
	if raw.Lflag&(unix.ICANON|unix.ECHO) != 0 || raw.Oflag&unix.OPOST != 0 || raw.Iflag&(unix.ICRNL|unix.IXON) != 0 {
		t.Fatalf("not raw: %+v", raw)
	}
	if raw.Cflag&unix.CSIZE != unix.CS8 || raw.Cflag&unix.PARENB == 0 || raw.Cflag&unix.PARODD != 0 ||
		raw.Cflag&unix.CSTOPB == 0 || raw.Cflag&unix.CRTSCTS == 0 || raw.Cflag&unix.CBAUD != unix.B115200 ||
		raw.Ispeed != unix.B115200 || raw.Cflag&unix.HUPCL == 0 || raw.Cflag&(unix.CREAD|unix.CLOCAL) != unix.CREAD|unix.CLOCAL {
		t.Fatalf("line settings: %+v", raw)
	}
	if raw.Cc[unix.VMIN] != 0 || raw.Cc[unix.VTIME] != 0 {
		t.Fatal("blocking read settings")
	}
	soft := (&serialSettings{Baud: 9600, Parity: "odd", Flow: "soft", DataBits: 7, StopBits: 1}).termios(base)
	if soft.Iflag&(unix.IXON|unix.IXOFF) != unix.IXON|unix.IXOFF || soft.Cflag&unix.CRTSCTS != 0 ||
		soft.Cflag&(unix.PARENB|unix.PARODD) != unix.PARENB|unix.PARODD || soft.Cflag&unix.CSIZE != unix.CS7 ||
		soft.Cflag&unix.CSTOPB != 0 || soft.Cflag&unix.CBAUD != unix.B9600 {
		t.Fatalf("soft flow settings: %+v", soft)
	}
}
