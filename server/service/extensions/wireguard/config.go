package wireguard

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Validate accepts declarative wg-quick profiles, never executable hooks.
// AllowedIPs selects/authorizes peers; only Address creates connected routes.
// Returning only field names in errors keeps uploaded keys out of API responses.
func Validate(data string) (string, error) {
	if len(data) > 64*1024 || strings.ContainsRune(data, 0) {
		return "", fmt.Errorf("configuration is too large or contains invalid characters")
	}
	section, peers := "", 0
	seenInterface, privateKey, address, peerKey, allowed := false, false, false, false, false
	seen := map[string]bool{}
	var lines []string
	bad := func(key string) (string, error) { return "", fmt.Errorf("invalid or unsupported field: %s", key) }
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if section == "Peer" && (!peerKey || !allowed) {
				return "", fmt.Errorf("each peer requires PublicKey and AllowedIPs")
			}
			switch line {
			case "[Interface]":
				if seenInterface || peers != 0 {
					return "", fmt.Errorf("exactly one Interface must precede peers")
				}
				seenInterface = true
				section = "Interface"
			case "[Peer]":
				if !seenInterface {
					return "", fmt.Errorf("Interface must precede peers")
				}
				peers++
				section = "Peer"
				peerKey = false
				allowed = false
			default:
				return "", fmt.Errorf("unsupported configuration section")
			}
			seen = map[string]bool{}
			lines = append(lines, line)
			if section == "Interface" {
				lines = append(lines, "Table = off")
			}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || section == "" {
			return "", fmt.Errorf("invalid configuration line")
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if len(key) > 32 {
			return "", fmt.Errorf("invalid field name")
		}
		if seen[key] && key != "Address" && key != "DNS" && key != "AllowedIPs" {
			return bad(key)
		}
		seen[key] = true
		valid := false
		if section == "Interface" {
			switch key {
			case "PrivateKey":
				valid = validKey(value)
				privateKey = valid
			case "Address":
				valid = validPrefixes(value)
				address = address || valid
			case "ListenPort":
				valid = validNumber(value, 0, 65535)
			case "MTU":
				valid = validNumber(value, 1280, 9000)
			case "DNS":
				valid = true
				for _, v := range strings.Split(value, ",") {
					if _, e := netip.ParseAddr(strings.TrimSpace(v)); e != nil {
						valid = false
					}
				}
			case "Table":
				valid = value == "auto" || value == "off" || validNumber(value, 1, 4294967295)
			}
		} else {
			switch key {
			case "PublicKey":
				valid = validKey(value)
				peerKey = valid
			case "PresharedKey":
				valid = validKey(value)
			case "AllowedIPs":
				valid = validPrefixes(value)
				allowed = allowed || valid
			case "PersistentKeepalive":
				valid = validNumber(value, 0, 65535)
			case "Endpoint":
				host, port, e := net.SplitHostPort(value)
				valid = e == nil && validNumber(port, 1, 65535) && validHost(host)
			}
		}
		if !valid {
			return bad(key)
		}
		// Always keep the routing policy above, including for imported Table=auto
		// or numeric tables. Preserve AllowedIPs itself without narrowing it.
		if section == "Interface" && key == "Table" {
			continue
		}
		lines = append(lines, key+" = "+value)
	}
	if !privateKey || !address || peers == 0 || !peerKey || !allowed {
		return "", fmt.Errorf("Interface requires PrivateKey and Address; each Peer requires PublicKey and AllowedIPs")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func validKey(s string) bool {
	b, e := base64.StdEncoding.DecodeString(s)
	return e == nil && len(b) == 32
}
func validNumber(s string, min, max uint64) bool {
	n, e := strconv.ParseUint(s, 10, 32)
	return e == nil && n >= min && n <= max
}
func validPrefixes(s string) bool {
	for _, p := range strings.Split(s, ",") {
		if _, e := netip.ParsePrefix(strings.TrimSpace(p)); e != nil {
			return false
		}
	}
	return true
}
func validHost(s string) bool {
	if _, e := netip.ParseAddr(s); e == nil {
		return true
	}
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// profileConfig applies the UI routing choice independently of imported Table.
func profileConfig(data string, routeAllowedIPs bool) (string, error) {
	normalized, err := Validate(data)
	if err != nil {
		return "", err
	}
	if routeAllowedIPs {
		normalized = strings.Replace(normalized, "Table = off\n", "Table = auto\n", 1)
	}
	return normalized, nil
}
