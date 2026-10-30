package logs

import (
	"net/netip"
	"regexp"
	"strings"
)

// The developer archive leaves the device: addresses and network names are
// of no use to the developers and identify the user's network. Loopback,
// unspecified and netmask-shaped values stay, so routes remain readable.
var (
	maskMAC  = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?:[:-][0-9a-f]{2}){5}\b`)
	maskIPv4 = regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1?[0-9]?[0-9])){3}\b`)
	// Candidates only: each is validated as an address before masking.
	maskIPv6 = regexp.MustCompile(`(?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:%[a-z0-9]+)?`)
	// "SSID: Home", "ssid=\"Home\"" on a line of their own (iw, wpa_supplicant
	// configuration dumps), and "SSID 'Home'" / "ssid=Home" inside log messages.
	maskSSIDLine   = regexp.MustCompile(`(?im)^(\s*(?:ssid|essid)\s*[:=]\s*).*$`)
	maskSSIDInline = regexp.MustCompile(`(?i)\b(e?ssid)(\s*[:=]\s*|\s+)('[^'\n]*'|"[^"\n]*"|[^\s,;\]\)]+)`)
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
	text = maskIPv6.ReplaceAllStringFunc(text, func(candidate string) string {
		bare, _, _ := strings.Cut(candidate, "%")
		addr, err := netip.ParseAddr(bare)
		// Not an address (a time such as 12:34:56), or loopback/unspecified.
		if err != nil || addr.IsLoopback() || addr.IsUnspecified() {
			return candidate
		}
		return "<ip6>"
	})
	text = maskSSIDLine.ReplaceAllString(text, "${1}<ssid>")
	return maskSSIDInline.ReplaceAllString(text, "${1}${2}<ssid>")
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
