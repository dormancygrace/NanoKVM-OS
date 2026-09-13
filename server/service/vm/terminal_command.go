package vm

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var terminalPortPattern = regexp.MustCompile(`^/dev/(tty(S|USB|ACM|GS)[0-9]+|serial[0-9]+)$`)

// Launch serial directly: no login shell, echoed command, or shell expansion.
func terminalCommand(q url.Values) (*exec.Cmd, error) {
	if !q.Has("port") {
		return exec.Command("/bin/sh", "-l"), nil
	}
	port := strings.TrimSpace(q.Get("port"))
	if !terminalPortPattern.MatchString(port) {
		return nil, fmt.Errorf("invalid serial port")
	}
	args := []string{port, "--quiet", "--imap", "lfcrlf", "--noreset"}
	if strings.HasPrefix(port, "/dev/ttyGS") {
		// CDC ACM line coding is nominal, not a USB throughput limit. Still set
		// raw 8N1/no-flow mode so inherited termios cannot swallow stream bytes.
		args = append(args, "--baud", "9600", "--parity", "none", "--flow", "none", "--databits", "8", "--stopbits", "1")
	} else {
		options := []struct{ key, flag, fallback, allowed string }{
			{"baud", "--baud", "115200", "50 75 110 134 150 200 300 600 1200 1800 2400 4800 9600 19200 38400 57600 115200 230400 460800 500000 576000 921600 1000000 1152000 1500000 2000000 2500000 3000000 3500000 4000000"},
			{"parity", "--parity", "none", "n o e none even odd"},
			{"flowControl", "--flow", "none", "n h s x none soft hard"},
			{"dataBits", "--databits", "8", "5 6 7 8"},
			{"stopBits", "--stopbits", "1", "1 2"},
		}
		for _, option := range options {
			value := strings.ToLower(strings.TrimSpace(q.Get(option.key)))
			if value == "" {
				value = option.fallback
			}
			if strings.ContainsAny(value, " \t\r\n") || !strings.Contains(" "+option.allowed+" ", " "+value+" ") {
				return nil, fmt.Errorf("invalid serial %s", option.key)
			}
			args = append(args, option.flag, value)
		}
	}
	path := "/kvmapp/system/bin/picocom"
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0111 == 0 {
		path = "picocom"
	}
	return exec.Command(path, args...), nil
}
