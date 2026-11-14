package openvpn

import (
	"strings"
	"testing"
)

const profile = "client\ndev tun\nremote vpn.example.test 1194 udp\nauth-user-pass\n<ca>\nCA-TEST\n</ca>\n"

func TestNormalizeAndRejectExecutableInputs(t *testing.T) {
	p, e := Normalize(profile, nil)
	if e != nil || !p.NeedsAuth || strings.Contains(p.Config, "auth-user-pass") {
		t.Fatal("login profile must defer credentials to the service", e)
	}
	if strings.Contains(p.Config, "dev tun") {
		t.Fatal("the service must own the runtime device name")
	}
	if _, e = Normalize(p.Config, nil); e != nil {
		t.Fatal("normalized profile not stable", e)
	}
	for _, line := range []string{"up /bin/sh", "plugin /tmp/injected.so", "config /etc/private", "log /etc/shadow", "management /tmp/socket unix", "dev null", "dev-type tunnel", "ca ../root.key", "auth-user-pass /etc/passwords", "script-security 3", "setenv LD_PRELOAD /tmp/x"} {
		if _, e := Normalize(profile+line+"\n", nil); e == nil {
			t.Error("accepted", line)
		}
	}
	if _, e = Normalize(strings.Replace(profile, "remote vpn.example.test 1194 udp", "<connection>\nremote vpn.example.test 443 tcp\n</connection>", 1), nil); e != nil {
		t.Fatal(e)
	}
}

// TAP: by the device name or by dev-type (which wins); the type survives
// normalizing again (the service does it at every start), and a TUN profile
// carries no type line.
func TestTAPProfile(t *testing.T) {
	for src, tap := range map[string]bool{"dev tap0\n": true, "dev tun\ndev-type tap\n": true, "dev tap\ndev-type tun\n": false, "": false} {
		p, e := Normalize(strings.Replace(profile, "dev tun\n", src, 1), nil)
		if e != nil || p.TAP != tap || strings.Contains(p.Config, "dev-type") != tap {
			t.Fatal(src, e, p.TAP)
		}
		if again, e := Normalize(p.Config, nil); e != nil || again.TAP != tap || strings.Count(again.Config, "dev-type") > 1 {
			t.Fatal("type not kept:", src, e)
		}
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

func TestNormalizeRejectsLinesOpenVPNWouldSplit(t *testing.T) {
	// A closing tag past the 256-byte read chunk would end the block early in
	// OpenVPN and turn the following lines into directives.
	smuggled := strings.Replace(profile, "CA-TEST\n", strings.Repeat("A", 251)+"</ca>\nup /bin/sh\n<ca>\nCA-TEST\n", 1)
	if _, e := Normalize(smuggled, nil); e == nil {
		t.Fatal("accepted a closing tag inside a credential line")
	}
	long := strings.Replace(profile, "remote vpn.example.test 1194 udp", "remote "+strings.Repeat("a", 260)+" 1194 udp", 1)
	if _, e := Normalize(long, nil); e == nil {
		t.Fatal("accepted a profile line longer than OpenVPN reads whole")
	}
	asset := map[string]string{"root.crt": strings.Repeat("B", 300)}
	if _, e := Normalize("client\ndev tun\nremote vpn.example.test\nca root.crt\n", asset); e == nil {
		t.Fatal("accepted a credential file line longer than OpenVPN reads whole")
	}
	pem := "-----BEGIN CERTIFICATE-----\n" + strings.Repeat(strings.Repeat("C", 64)+"\n", 4) + "-----END CERTIFICATE-----"
	if _, e := Normalize("client\ndev tun\nremote vpn.example.test\nca root.crt\n", map[string]string{"root.crt": pem}); e != nil {
		t.Fatal("rejected an ordinary PEM certificate:", e)
	}
}
