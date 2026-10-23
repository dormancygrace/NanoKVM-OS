package network

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/utils"
)

// The AP routes against the real kernel: a server namespace (this test's,
// from unshare -n) with eth0 and wlan0, a client on each. The client on
// Ethernet routes the AP subnet through the device's Ethernet address; its
// packets reach the AP address but arrive on eth0, and must be refused
// without counting towards the lockout. Both address families, and IPv4 on
// the dual-stack listener the server uses.
//
// NANOKVM_NETNS_TEST=1 unshare -n go test -run InANetworkNamespace (as root)
func TestApRoutesInANetworkNamespace(t *testing.T) {
	if os.Getenv("NANOKVM_NETNS_TEST") != "1" {
		t.Skip("needs NANOKVM_NETNS_TEST=1 inside a network namespace")
	}
	if os.Getenv("NANOKVM_AP_CLIENT") != "" {
		t.Skip("client helper")
	}
	suffix := fmt.Sprint(os.Getpid() % 100000)
	ethNS, apNS := "nkt-eth"+suffix, "nkt-ap"+suffix
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	for _, ns := range []string{ethNS, apNS} {
		run("ip", "netns", "add", ns)
		t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
	}
	run("ip", "link", "set", "lo", "up")
	for _, link := range []struct{ name, peer, ns, addr4, peer4, addr6, peer6 string }{
		{"eth0", "c0", ethNS, "192.0.2.1/24", "192.0.2.2/24", "fd00:1::1/64", "fd00:1::2/64"},
		{"wlan0", "c1", apNS, "10.23.42.1/24", "10.23.42.2/24", "fd00:2::1/64", "fd00:2::2/64"},
	} {
		// Other tests in this disposable namespace may have made one.
		exec.Command("ip", "link", "del", link.name).Run()
		run("ip", "link", "add", link.name, "type", "veth", "peer", "name", link.peer)
		run("ip", "link", "set", link.peer, "netns", link.ns)
		run("ip", "addr", "add", link.addr4, "dev", link.name)
		run("ip", "-6", "addr", "add", link.addr6, "dev", link.name, "nodad")
		run("ip", "link", "set", link.name, "up")
		run("ip", "netns", "exec", link.ns, "ip", "link", "set", "lo", "up")
		run("ip", "netns", "exec", link.ns, "ip", "addr", "add", link.peer4, "dev", link.peer)
		run("ip", "netns", "exec", link.ns, "ip", "-6", "addr", "add", link.peer6, "dev", link.peer, "nodad")
		run("ip", "netns", "exec", link.ns, "ip", "link", "set", link.peer, "up")
	}
	// The Ethernet client sends the AP subnet through the device.
	run("ip", "netns", "exec", ethNS, "ip", "route", "add", "10.23.42.0/24", "via", "192.0.2.1")
	run("ip", "netns", "exec", ethNS, "ip", "-6", "route", "add", "fd00:2::/64", "via", "fd00:1::1")

	oldMode, oldPassword, oldPenalty := apMode, apPassword, apPenalty
	apMode = func() bool { return true }
	apPassword = func() string { return "rightkey" }
	apPenalty = 0
	apGuard = apKeyGuard{}
	t.Cleanup(func() { apMode, apPassword, apPenalty = oldMode, oldPassword, oldPenalty })

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/api/network/wifi/verify", (&Service{}).VerifyApLogin)
	listener, err := net.Listen("tcp", "[::]:0") // dual stack, as the server listens
	if err != nil {
		t.Fatal(err)
	}
	server := utils.NewHTTPServer("", engine, false)
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	verifyFrom := func(ns, host, key string) string {
		t.Helper()
		cmd := exec.Command("ip", "netns", "exec", ns, self, "-test.run", "^TestApClientHelper$")
		cmd.Env = append(os.Environ(), "NANOKVM_AP_CLIENT="+net.JoinHostPort(host, fmt.Sprint(port)), "NANOKVM_AP_KEY="+key)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("client in %s to %s: %v\n%s", ns, host, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if body, ok := strings.CutPrefix(line, "BODY "); ok {
				return body
			}
		}
		t.Fatalf("client in %s to %s printed no response:\n%s", ns, host, out)
		return ""
	}
	code := func(body string) string {
		i := strings.Index(body, `"code":`)
		if i < 0 {
			return body
		}
		end := strings.IndexAny(body[i+7:], ",}")
		return body[i+7 : i+7+end]
	}

	for _, ap := range []string{"10.23.42.1", "fd00:2::1"} {
		// Off the AP: refused as "invalid mode", whatever the key, and
		// never counted; more than the lockout budget of them.
		for range apKeyMaxFailures + 2 {
			if got := code(verifyFrom(ethNS, ap, "wrongkey")); got != "-1" {
				t.Fatalf("Ethernet client to %s with a wrong key: code %s, want -1", ap, got)
			}
		}
		if got := code(verifyFrom(ethNS, ap, "rightkey")); got != "-1" {
			t.Fatalf("Ethernet client to %s with the right key: code %s, want -1", ap, got)
		}
		// On the AP: the right key works; the guesses above left no lockout.
		if got := code(verifyFrom(apNS, ap, "rightkey")); got != "0" {
			t.Fatalf("AP client to %s: code %s, want 0", ap, got)
		}
		if got := code(verifyFrom(apNS, ap, "wrongkey")); got != "-4" {
			t.Fatalf("AP client to %s with a wrong key: code %s, want -4", ap, got)
		}
	}
}

// TestApClientHelper is the client the namespace test runs with ip netns exec.
func TestApClientHelper(t *testing.T) {
	target := os.Getenv("NANOKVM_AP_CLIENT")
	if target == "" {
		t.Skip("run by TestApRoutesInANetworkNamespace")
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+target+"/api/network/wifi/verify", nil)
	req.Header.Set("X-AP-Key", os.Getenv("NANOKVM_AP_KEY"))
	client := &http.Client{Timeout: 10 * time.Second}
	rsp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer rsp.Body.Close()
	body, _ := io.ReadAll(rsp.Body)
	fmt.Printf("BODY %s\n", body)
}
