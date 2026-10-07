package network

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// wpa_cli's default control directory; wpa_cli -i wlan0 talks to this socket.
const wpaControlSocket = "/var/run/wpa_supplicant/wlan0"

// wpa_cli waits ten seconds for a reply and reads at most 4 KiB.
const (
	wpaControlTimeout = 10 * time.Second
	wpaReplyLimit     = 4096
)

var wpaClientSequence atomic.Uint64

// wpaControl returns a client for wpa_supplicant's control socket. A request
// gets the same reply wpa_cli prints for the command, without starting a
// process on every Wi-Fi status poll.
func wpaControl(socket string) func(string) (string, error) {
	return func(command string) (string, error) {
		// wpa_supplicant replies to the sender's address, so the client socket
		// needs a name; the abstract namespace leaves no file behind.
		local := &net.UnixAddr{Net: "unixgram", Name: fmt.Sprintf("@nanokvm-wpa-%d-%d", os.Getpid(), wpaClientSequence.Add(1))}
		conn, err := net.DialUnix("unixgram", local, &net.UnixAddr{Net: "unixgram", Name: socket})
		if err != nil {
			return "", err
		}
		defer conn.Close()
		if err = conn.SetDeadline(time.Now().Add(wpaControlTimeout)); err != nil {
			return "", err
		}
		if _, err = conn.Write([]byte(command)); err != nil {
			return "", err
		}
		reply := make([]byte, wpaReplyLimit)
		for {
			n, err := conn.Read(reply)
			if err != nil {
				return "", err
			}
			// Like wpa_ctrl_request, skip event messages ("<level>text"); they
			// only reach monitors that attached, which this client never does.
			if text := string(reply[:n]); !strings.HasPrefix(text, "<") {
				return text, nil
			}
		}
	}
}
