package logs

import (
	"regexp"
	"strings"
)

// The developer archive leaves the device: addresses and network names are
// of no use to the developers and identify the user's network. Loopback,
// unspecified and netmask-shaped values stay, so routes remain readable.
var (
	maskMAC  = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?:[:-][0-9a-f]{2}){5}\b`)
	maskIPv4 = regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])){3}\b`)
	maskIPv6 = regexp.MustCompile(`(?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:%[a-z0-9]+)?`)
	maskSSID = regexp.MustCompile(`(?im)^(\s*(?:ssid|essid)\s*[:=]\s*).*$`)
	// Settings keys whose values are or name credentials; the key stays.
	configSecret = regexp.MustCompile(`(?im)^(\s*"?[a-z0-9_.-]*(?:cred|pass|secret|token|key|user)[a-z0-9_.-]*"?\s*[:=]\s*).+$`)
)

// MaskAddresses replaces IPv4, IPv6 and MAC addresses and Wi-Fi network
// names with placeholders.
func MaskAddresses(text string) string {
	text = maskMAC.ReplaceAllString(text, "<mac>")
	text = maskIPv4.ReplaceAllStringFunc(text, func(ip string) string {
		if strings.HasPrefix(ip, "127.") || strings.HasPrefix(ip, "255.") || ip == "0.0.0.0" {
			return ip
		}
		return "<ip>"
	})
	text = maskIPv6.ReplaceAllStringFunc(text, func(ip string) string {
		bare, _, _ := strings.Cut(ip, "%")
		if bare == "::" || bare == "::1" {
			return ip
		}
		// Times (12:34:56) and similar: decimal groups and no "::".
		if !strings.Contains(bare, "::") && strings.Count(bare, ":") < 3 {
			return ip
		}
		if !strings.ContainsAny(strings.ToLower(bare), "abcdef") && !strings.Contains(bare, "::") {
			return ip
		}
		return "<ip6>"
	})
	return maskSSID.ReplaceAllString(text, "${1}<ssid>")
}

// SanitizeFile prepares a whole text file for the developer archive: entries
// that may hold credentials are hidden as in the log viewer, then addresses
// are masked. Like the viewer it keeps the last MaxLines/MaxBytes.
func SanitizeFile(data []byte) string {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(append(make([]byte, 0, len(data)+1), data...), '\n')
	}
	text, _, _ := sanitizeTail(data, false)
	if text == "" {
		return ""
	}
	return MaskAddresses(text) + "\n"
}

// SanitizeConfig is SanitizeFile for a settings file: values of keys that name
// credentials, users or keys are replaced before the line checks run.
func SanitizeConfig(data []byte) string {
	return SanitizeFile([]byte(configSecret.ReplaceAllString(string(data), "${1}<hidden>")))
}
