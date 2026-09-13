package vm

import (
	"net/url"
	"strings"
	"testing"
)

func TestTerminalSerialCommand(t *testing.T) {
	for _, query := range []string{"port=/dev/ttyGS0", "port=/dev/ttyGS0&baud=9600", "port=/dev/ttyGS0&baud=115200"} {
		q, _ := url.ParseQuery(query)
		cmd, err := terminalCommand(q)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--quiet") || strings.Contains(args, "--nolock") || !strings.Contains(args, "--baud 9600 ") || strings.Contains(args, "/bin/sh") {
			t.Fatalf("unexpected USB command: %s", args)
		}
	}
	q, _ := url.ParseQuery("port=/dev/ttyS1&baud=9600&parity=even")
	cmd, err := terminalCommand(q)
	if err != nil || !strings.Contains(strings.Join(cmd.Args, " "), "--baud 9600 --parity even") {
		t.Fatalf("UART settings lost: %v %v", cmd, err)
	}
	for _, query := range []string{"port=/etc/passwd", "port=/dev/ttyS1%3Bid", "port=/dev/ttyS1&baud=12345", "port=/dev/ttyS1&parity=none%20even", "port="} {
		q, _ := url.ParseQuery(query)
		if _, err := terminalCommand(q); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
	cmd, err = terminalCommand(url.Values{})
	if err != nil || cmd.Path != "/bin/sh" {
		t.Fatal("shell terminal changed")
	}
}
