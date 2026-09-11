package openvpn

import (
	"strings"
	"testing"
)

const profile = "client\ndev tun\nremote vpn.example.test 1194 udp\nauth-user-pass\n<ca>\nCA-TEST\n</ca>\n"

func TestNormalizeAndRejectExecutableInputs(t *testing.T) {
	p, e := Normalize(profile, nil)
	if e != nil || !p.NeedsAuth || !strings.Contains(p.Config, "auth-user-pass") {
		t.Fatal("login profile", e)
	}
	if _, e = Normalize(p.Config, nil); e != nil {
		t.Fatal("normalized profile not stable", e)
	}
	for _, line := range []string{"up /bin/sh", "plugin /tmp/injected.so", "config /etc/private", "log /etc/shadow", "management /tmp/socket unix", "dev tap0", "ca ../root.key", "auth-user-pass /etc/passwords", "script-security 3", "setenv LD_PRELOAD /tmp/x"} {
		if _, e := Normalize(profile+line+"\n", nil); e == nil {
			t.Error("accepted", line)
		}
	}
	if _, e = Normalize(strings.Replace(profile, "remote vpn.example.test 1194 udp", "<connection>\nremote vpn.example.test 443 tcp\n</connection>", 1), nil); e != nil {
		t.Fatal(e)
	}
}
func TestReferencedCredentials(t *testing.T) {
	src := "client\ndev tun\nremote vpn.example.test\nca root.crt\ncert client.crt\nkey client.key\ntls-auth ta.key 1\n"
	p, e := Normalize(src, map[string]string{"root.crt": "TEST-CA", "client.crt": "TEST-CERT", "client.key": "TEST-KEY", "ta.key": "TEST-STATIC"})
	if e != nil || !strings.Contains(p.Config, "<key>\nTEST-KEY\n</key>") || !strings.Contains(p.Config, "key-direction 1") {
		t.Fatal(e)
	}
	if _, e = Normalize(src, nil); e == nil {
		t.Fatal("missing file accepted")
	}
	if _, e = Normalize(src, map[string]string{"root.crt": strings.Repeat("a", 262144), "client.crt": "c", "client.key": "k", "ta.key": "s"}); e == nil {
		t.Fatal("combined size limit missing")
	}
}
