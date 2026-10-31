package recovery

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"NanoKVM-Server/authn"
	"gopkg.in/yaml.v3"
)

// Paths are relative to the filesystem root. Hardware detection records,
// kernel/boot binaries, package databases, disks and arbitrary /etc are excluded.
var settings = strings.Fields(`
 etc/kvm/frame_detect etc/kvm/encoder_codec etc/kvm/input-region.json etc/kvm/leader-key
 etc/kvm/mouse-jiggler etc/kvm/oled_sleep etc/kvm/memory.conf etc/kvm/GOMEMLIMIT
 etc/kvm/web-title etc/kvm/update-check.disabled etc/kvm/cpufreq etc/kvm/cpufreq.boot
 etc/kvm/monitor_resolution etc/kvm/monitor_portrait
 etc/kvm/monitor_portrait_resolution etc/kvm/monitor_edid_sha256
 etc/kvm/shortcuts.json etc/kvm/server.yaml etc/kvm/ssh_stop etc/kvm/mdns_disabled
 etc/kvm/hevc_vi_direct etc/kvm/hdmi_disable etc/kvm/hdmi_idle_timeout
 etc/kvm/mcp.json etc/kvm/ai-control.mode etc/kvm/autostart.list
 etc/kvm/wifi.ssid etc/kvm/wifi.pass etc/kvm/wifi.disabled etc/kvm/wifi.band_preference
 etc/kvm/usb-before-rustdesk.json
 etc/kvm/picoclaw-runtime.json etc/kvm/pwd
 kvmapp/kvm/fps kvmapp/kvm/qlty kvmapp/kvm/res kvmapp/kvm/type
 kvmapp/kvm/transport kvmapp/kvm/mjpeg_chroma kvmapp/kvm/ap.pass
 boot/hostname.prefix boot/eth.mac boot/eth.disabled boot/eth.nodhcp boot/eth.vlan boot/hostname
 boot/wifi.ssid boot/wifi.pass boot/wifi.disabled boot/resolv.conf
 boot/resolv.conf.manual.bak boot/sshd_config
 etc/kvm/cron/crontabs/root etc/kvm/cache/wol etc/hostname etc/hosts etc/timezone etc/localtime etc/chrony.conf etc/kvm/date-time.json
 root/.ssh/authorized_keys
 root/.picoclaw/config.json root/.picoclaw/.security.yml
 etc/ssh/ssh_host_ed25519_key etc/ssh/ssh_host_ed25519_key.pub
 etc/ssh/ssh_host_rsa_key etc/ssh/ssh_host_rsa_key.pub
 etc/ssh/ssh_host_ecdsa_key etc/ssh/ssh_host_ecdsa_key.pub
 etc/kvm/server.crt etc/kvm/server.key etc/kvm/bmc/config.json
 etc/kvm/acme/settings.json var/lib/tailscale/tailscaled.state etc/netbird/config.json
 etc/kvm/notify/config.json etc/kvm/notify/addresses.json
`)
var trees = []string{"etc/kvm/network", "etc/kvm/wifi.saved", "etc/kvm/usb", "etc/kvm/branding", "etc/kvm/scripts", "etc/kvm/wireguard", "etc/kvm/openvpn", "etc/kvm/tls", "etc/kvm/acme/data", "etc/nanokvm-rustdesk"}
var packageName = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9+_.-]{0,127}$`)
var versionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-._][A-Za-z0-9.-]+)?$`)

func allowed(p string) bool {
	if p == "" || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, s := range settings {
		if p == s {
			return true
		}
	}
	for _, s := range trees {
		if strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}
func users(p string) bool { return p == "etc/kvm/pwd" || p == "root/.ssh/authorized_keys" }
func portable(p string) bool {
	// A clone retains its own credentials, identity, network and hardware policy.
	if users(p) || strings.HasPrefix(p, "etc/ssh/") || strings.HasPrefix(p, "boot/") || strings.HasPrefix(p, "etc/kvm/wifi") || strings.HasPrefix(p, "etc/kvm/network/") || strings.HasPrefix(p, "etc/kvm/monitor_") {
		return false
	}
	for _, prefix := range []string{"etc/kvm/wireguard/", "etc/kvm/openvpn/", "etc/kvm/tls/", "etc/kvm/acme/", "etc/nanokvm-rustdesk/", "root/.picoclaw/", "etc/kvm/usb/"} {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}
	switch p {
	case "etc/kvm/bmc/config.json", "etc/kvm/cron/crontabs/root", "etc/kvm/cache/wol", "etc/netbird/config.json", "var/lib/tailscale/tailscaled.state", "etc/kvm/acme/settings.json", "etc/kvm/server.yaml", "etc/kvm/server.crt", "etc/kvm/server.key", "etc/hostname", "etc/hosts", "etc/kvm/ssh_stop", "etc/kvm/usb-before-rustdesk.json", "etc/kvm/mcp.json", "etc/kvm/autostart.list", "etc/kvm/picoclaw-runtime.json",
		// Notification channels hold tokens and passwords; the addresses are
		// this device's login history.
		"etc/kvm/notify/config.json", "etc/kvm/notify/addresses.json":
		return false
	}
	return !strings.HasPrefix(p, "etc/kvm/scripts/")
}
func Validate(a Archive) error {
	if a.Format != 1 || !versionPattern.MatchString(a.Version) {
		return errors.New("unsupported archive format or application version")
	}
	if len(a.Files) > MaxFiles || len(a.Packages) > 2048 || len(a.Board) > 128 {
		return errors.New("archive inventory too large")
	}
	if len(a.RootHash) > 1024 || strings.ContainsAny(a.RootHash, ":\r\n\x00") {
		return errors.New("invalid root password record")
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range a.Files {
		total += len(f.Data)
		if !allowed(f.Path) || seen[f.Path] || len(f.Data) > MaxFile || total > MaxData || f.Mode&^0777 != 0 {
			return fmt.Errorf("invalid or excessive settings entry: %s", f.Path)
		}
		seen[f.Path] = true
		if f.Path == "etc/kvm/pwd" {
			if err := authn.ValidateRecoveryAccounts(f.Data); err != nil {
				return err
			}
		}
		if f.Path == "etc/kvm/server.yaml" {
			var c struct {
				Port struct {
					HTTP  int `yaml:"http"`
					HTTPS int `yaml:"https"`
				} `yaml:"port"`
			}
			if yaml.Unmarshal(f.Data, &c) != nil || c.Port.HTTP < 1 || c.Port.HTTP > 65535 || c.Port.HTTPS < 1 || c.Port.HTTPS > 65535 {
				return errors.New("invalid server configuration")
			}
		}
	}
	for _, s := range a.Packages {
		if !packageName.MatchString(s) {
			return errors.New("invalid package inventory")
		}
	}
	return nil
}
