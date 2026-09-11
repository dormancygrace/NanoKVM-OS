package openvpn

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

type Parsed struct {
	Config    string
	NeedsAuth bool
}

// Normalize resolves uploaded credential files into a self-contained profile.
// Only client options are accepted; imported scripts/plugins/management paths
// must never become root commands or arbitrary local file reads.
func Normalize(data string, assets map[string]string) (Parsed, error) {
	var result Parsed
	if len(data) > 256*1024 || strings.ContainsRune(data, 0) {
		return result, fmt.Errorf("invalid or oversized OpenVPN profile")
	}
	allowed := strings.Fields("client tls-client dev dev-type proto remote remote-random remote-random-hostname resolv-retry nobind bind persist-key persist-tun remote-cert-tls verify-x509-name peer-fingerprint cipher data-ciphers data-ciphers-fallback auth auth-nocache auth-retry reneg-sec reneg-bytes reneg-pkts key-direction tls-version-min tls-version-max tls-cipher tls-ciphersuites tls-cert-profile tls-timeout verb mute mute-replay-warnings connect-retry connect-retry-max connect-timeout server-poll-timeout hand-window tran-window ping ping-restart ping-exit keepalive sndbuf rcvbuf tun-mtu link-mtu mssfix fragment explicit-exit-notify float fast-io route route-ipv6 route-gateway route-metric route-delay route-nopull redirect-gateway redirect-private pull pull-filter dhcp-option block-outside-dns comp-lzo compress allow-compression socket-flags tcp-nodelay disable-dco setenv setenv-safe push-peer-info keysize ns-cert-type ignore-unknown-option tun-mtu-extra auth-user-pass ca cert key tls-auth tls-crypt tls-crypt-v2 crl-verify extra-certs")
	permit := map[string]bool{}
	for _, s := range allowed {
		permit[s] = true
	}
	inline := map[string]bool{"ca": true, "cert": true, "key": true, "tls-auth": true, "tls-crypt": true, "tls-crypt-v2": true, "crl-verify": true, "extra-certs": true}
	section := ""
	block := []string{}
	output := []string{}
	remote, trust, serverCheck := false, false, false
	connection := false
	lineError := func(n int, msg string) (Parsed, error) { return Parsed{}, fmt.Errorf("line %d: %s", n, msg) }
	for number, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if section != "" {
			if line == "</"+section+">" {
				output = append(output, "<"+section+">", strings.Join(block, "\n"), line)
				if section == "ca" {
					trust = true
				}
				section = ""
				block = nil
				continue
			}
			if strings.HasPrefix(line, "<") {
				return lineError(number+1, "invalid credential block")
			}
			block = append(block, line)
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if line == "<connection>" {
			if connection {
				return lineError(number+1, "nested connection blocks are not supported")
			}
			connection = true
			output = append(output, line)
			continue
		}
		if line == "</connection>" {
			if !connection {
				return lineError(number+1, "unexpected connection block end")
			}
			connection = false
			output = append(output, line)
			continue
		}
		if strings.HasPrefix(line, "<") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "<"), ">")
			if !inline[name] || line != "<"+name+">" {
				return lineError(number+1, "unsupported inline block")
			}
			section = name
			continue
		}
		args, err := words(line)
		if err != nil || len(args) == 0 {
			return lineError(number+1, "invalid quoting")
		}
		key := strings.TrimPrefix(args[0], "--")
		args = args[1:]
		if !permit[key] {
			return lineError(number+1, "unsupported client option")
		}
		if inline[key] {
			if len(args) < 1 || len(args) > 2 || args[0] != filepath.Base(args[0]) || strings.ContainsAny(args[0], "/\\") {
				return lineError(number+1, "upload the referenced credential file alongside the .ovpn file")
			}
			asset, ok := assets[args[0]]
			if !ok {
				return lineError(number+1, "a referenced credential file is missing from the upload")
			}
			if strings.Contains(asset, "<") || strings.ContainsRune(asset, 0) {
				return lineError(number+1, "invalid credential file")
			}
			if len(args) == 2 {
				if key != "tls-auth" || (args[1] != "0" && args[1] != "1") {
					return lineError(number+1, "unsupported credential option")
				}
				output = append(output, "key-direction "+args[1])
			}
			output = append(output, "<"+key+">", strings.TrimSpace(asset), "</"+key+">")
			if key == "ca" {
				trust = true
			}
			continue
		}
		switch key {
		case "dev", "dev-type":
			if len(args) != 1 || (key == "dev-type" && args[0] != "tun") || (key == "dev" && !strings.HasPrefix(args[0], "tun")) {
				return lineError(number+1, "use a routed TUN profile")
			}
			continue
		case "client", "tls-client", "verb", "mute", "auth-retry", "ignore-unknown-option", "keysize":
			continue
		case "block-outside-dns":
			continue // Windows-only; DNS is managed by the Linux resolver.
		case "ns-cert-type":
			if len(args) != 1 || args[0] != "server" {
				return lineError(number+1, "unsupported certificate purpose")
			}
			key = "remote-cert-tls"
			serverCheck = true
		case "remote-cert-tls":
			if len(args) != 1 || args[0] != "server" {
				return lineError(number+1, "a server certificate is required")
			}
			serverCheck = true
		case "peer-fingerprint":
			trust = true
		case "auth-user-pass":
			if len(args) != 0 {
				return lineError(number+1, "enter the VPN username and password in the interface")
			}
			result.NeedsAuth = true
		case "remote":
			if len(args) < 1 || len(args) > 3 {
				return lineError(number+1, "invalid remote endpoint")
			}
			remote = true
		case "setenv", "setenv-safe":
			// These two compatibility hints are commonly emitted by OpenVPN Connect.
			if len(args) == 0 || (args[0] != "CLIENT_CERT" && args[0] != "opt") {
				return lineError(number+1, "unsupported environment option")
			}
			if args[0] == "opt" {
				if len(args) != 2 || args[1] != "block-outside-dns" {
					return lineError(number+1, "unsupported optional option")
				}
			}
			continue
		case "dhcp-option":
			if len(args) < 2 || (args[0] != "DNS" && args[0] != "DNS6" && args[0] != "DOMAIN" && args[0] != "DOMAIN-SEARCH") {
				return lineError(number+1, "unsupported DNS option")
			}
		}
		encoded := []string{key}
		for _, a := range args {
			encoded = append(encoded, strconv.Quote(a))
		}
		output = append(output, strings.Join(encoded, " "))
	}
	if section != "" || connection {
		return result, fmt.Errorf("unclosed configuration block")
	}
	if !remote || !trust {
		return result, fmt.Errorf("profile requires a remote endpoint and a CA certificate or peer fingerprint")
	}
	if !serverCheck {
		output = append(output, "remote-cert-tls server")
	}
	result.Config = "client\ndev tun\n" + strings.Join(output, "\n") + "\n"
	if len(result.Config) > 256*1024 {
		return Parsed{}, fmt.Errorf("combined profile and certificates exceed 256 KiB")
	}
	return result, nil
}

func words(line string) ([]string, error) {
	var out []string
	var value strings.Builder
	var quote rune
	escaped, started := false, false
	for _, c := range line {
		if escaped {
			value.WriteRune(c)
			escaped = false
			started = true
			continue
		}
		if c == '\\' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				value.WriteRune(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if unicode.IsSpace(c) {
			if started {
				out = append(out, value.String())
				value.Reset()
				started = false
			}
			continue
		}
		if (c == '#' || c == ';') && !started {
			break
		}
		value.WriteRune(c)
		started = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("invalid quoting")
	}
	if started {
		out = append(out, value.String())
	}
	return out, nil
}
