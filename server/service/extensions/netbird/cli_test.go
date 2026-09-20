package netbird

import (
	"strings"
	"sync"
	"testing"
)

func TestParseStatus(t *testing.T) {
	status, err := parseStatus(`diagnostic
{"fqdn":"nk.test","netbirdIp":"100.64.0.8/16","daemonVersion":"0.79.0","management":{"url":"https://api.netbird.io","connected":true},"signal":{"connected":true}}`)
	if err != nil || status.FQDN != "nk.test" || !status.Management.Connected {
		t.Fatalf("unexpected status: %#v, %v", status, err)
	}
}

func TestScanLoginURL(t *testing.T) {
	urls := make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	scanLoginURL(strings.NewReader("open https://app.netbird.io/setup?token=secret\n"), urls, &wg)
	wg.Wait()
	if got := <-urls; got != "https://app.netbird.io/setup?token=secret" {
		t.Fatalf("unexpected URL %q", got)
	}
}

func TestGetIPv4(t *testing.T) {
	if got := getIPv4("100.64.0.8/16"); got != "100.64.0.8" {
		t.Fatalf("unexpected IPv4 %q", got)
	}
	if got := getIPv4("fd00::1/64"); got != "" {
		t.Fatalf("unexpected IPv6 result %q", got)
	}
}
