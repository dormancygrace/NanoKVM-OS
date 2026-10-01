package logs

import (
	"regexp"
	"strings"
	"unicode"
)

const redactedLine = "[sensitive log entry hidden]"

// Unstructured logs cannot provide a complete secret inventory. Hide entire
// credential-bearing entries, rather than attempting to preserve their values.
var sensitiveEntry = regexp.MustCompile(`(?i)(password|passwd|passphrase|\bpsk\b|secret|token|credential|api[_ -]?key|auth[_ -]?key|authorization|cookie|bearer|private[_ -]?key|https?://[^\s/]+@|https?://[^\s]*(?:login\.tailscale\.com/a/|auth\.netbird\.io/)|[?&](?:key|signature|sig)=|ssh-(?:rsa|ed25519)|ecdsa-sha2|"(?:messages?|prompt|content|payload|body)"\s*:)`)
var pemMarker = regexp.MustCompile(`-----\s*(?:BEGIN|END) [A-Z0-9 ]+-----`)
var encodedKeyLine = regexp.MustCompile(`^\s*[A-Za-z0-9+/]{32,}={0,3}\s*$`)
var terminalEscape = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\))")

var sensitiveWords = []string{"password", "passwd", "passphrase", "psk", "secret", "token", "credential", "api_key", "api-key", "api key", "apikey", "auth_key", "auth-key", "auth key", "authkey", "authorization", "cookie", "bearer", "private_key", "private-key", "private key", "privatekey", "ssh-rsa", "ssh-ed25519", "ecdsa-sha2", "\"message", "\"prompt\"", "\"content\"", "\"payload\"", "\"body\"", "?key=", "&key=", "?signature=", "&signature=", "?sig=", "&sig="}

func sensitiveLine(line string) bool {
	lowered := strings.ToLower(line)
	for _, word := range sensitiveWords {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	// Most system messages have no URL. Keep the general URL checks off the
	// common path, where the large regexp would dominate a MiB snapshot.
	return strings.Contains(lowered, "http") && sensitiveEntry.MatchString(line)
}

func sanitizeTail(data []byte, truncated bool) (string, int, bool) {
	if truncated {
		data = trimPartialLine(data)
	}
	text := terminalEscape.ReplaceAllString(strings.ToValidUTF8(string(data), "�"), "")
	text = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, text)
	// Do not expose an unfinished last entry: a secret label may only arrive
	// with the following write. It will appear at the next refresh once complete.
	if last := strings.LastIndexByte(text, '\n'); last >= 0 {
		text = text[:last]
	} else {
		text = ""
	}
	if text == "" {
		return "", 0, truncated
	}
	lines := strings.Split(text, "\n")
	inPEM := false
	for i, line := range lines {
		marker := ""
		if strings.Contains(line, "-----") {
			marker = pemMarker.FindString(line)
		}
		hidden := inPEM || marker != "" || sensitiveLine(line) || encodedKeyLine.MatchString(line)
		if strings.Contains(marker, "BEGIN") {
			inPEM = true
		}
		if strings.Contains(marker, "END") {
			inPEM = false
		}
		if len(line) > 4096 {
			truncated = true
		}
		if hidden {
			lines[i] = redactedLine
		} else if len(line) > 4096 {
			lines[i] = "[long log entry omitted]"
			truncated = true
		}
	}
	if len(lines) > MaxLines {
		lines = lines[len(lines)-MaxLines:]
		truncated = true
	}
	// Redaction may expand short entries. Keep the response byte bound too.
	bytes := 0
	start := len(lines)
	for start > 0 && bytes+len(lines[start-1])+1 <= MaxBytes {
		start--
		bytes += len(lines[start]) + 1
	}
	if start > 0 {
		truncated = true
	}
	lines = lines[start:]
	return strings.Join(lines, "\n"), len(lines), truncated
}
