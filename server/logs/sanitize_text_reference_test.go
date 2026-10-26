package logs

import (
	"strings"
	"testing"
	"unicode"
)

// The reference freezes PR499's scalar control flow. Literal character and
// secret-redaction expectations remain covered by sanitize_fast_test.go.
func TestSanitizerTextEquivalence(t *testing.T) {
	inputs := []string{"", "a", "\n", "password=x\n", "-----BEGIN PRIVATE KEY-----\naaa\n-----END PRIVATE KEY-----\nsafe\n",
		"pass\u200bword=x\n", "to\x1b[31mken=x\n", "unicode Журнал 😀\n", strings.Repeat("a", 4097) + "\n",
		strings.Repeat("ordinary safe line\n", 10000)}
	for value := 0; value < 256; value++ {
		inputs = append(inputs, "safe:"+string([]byte{byte(value)})+"x\n")
	}
	for _, word := range sensitiveWords {
		for _, prefix := range []string{"", strings.Repeat("a", 16), strings.Repeat("a", 95)} {
			inputs = append(inputs, prefix+word+"=hidden\n", prefix+strings.ToUpper(word)+"=hidden\n")
		}
	}
	state := uint64(0xc906)
	for i := 0; i < 10000; i++ {
		n := i % 513
		data := make([]byte, n)
		for j := range data {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			data[j] = byte(state)
		}
		if n > 0 && i%2 == 0 {
			data[n-1] = '\n'
		}
		inputs = append(inputs, string(data))
	}
	for i, input := range inputs {
		for _, truncated := range []bool{false, true} {
			got, gl, gc := sanitizeTail([]byte(input), truncated)
			want, wl, wc := referenceSanitizeTail([]byte(input), truncated)
			if got != want || gl != wl || gc != wc {
				t.Fatalf("case%d truncated%v: got %q/%d/%v want %q/%d/%v", i, truncated, got, gl, gc, want, wl, wc)
			}
		}
	}
	t.Logf("%d inputs, two truncation modes match scalar PR", len(inputs))
}

func referenceSensitiveLine(line string) bool {
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

func referenceSanitizeTail(data []byte, truncated bool) (string, int, bool) {
	if truncated {
		data = trimPartialLine(data)
	}
	text := strings.ToValidUTF8(string(data), "�")
	// Most snapshots contain no ANSI escape sequences; avoid a regexp copy.
	if strings.IndexByte(text, 0x1b) >= 0 {
		text = terminalEscape.ReplaceAllString(text, "")
	}
	// Printable ASCII and the two retained whitespace bytes need no Unicode
	// classification. Keep the full filter for non-ASCII and control bytes.
	needsRuneFilter := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 0x7f || c < 0x20 && c != '\n' && c != '\t' {
			needsRuneFilter = true
			break
		}
	}
	if needsRuneFilter {
		text = strings.Map(func(r rune) rune {
			if r != '\n' && r != '\t' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
				return -1
			}
			return r
		}, text)
	}
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
		hidden := inPEM || marker != "" || referenceSensitiveLine(line) || encodedKeyLine.MatchString(line)
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

// Printable, nonperiodic lines exercise the production vector routing path,
// including keywords split by controls that disappear before redaction.
func routedSanitizerCorpus() []string {
	state := uint64(0x499500)
	var inputs []string
	for _, n := range []int{16, 17, 31, 32, 63, 64, 95, 127, 128, 129, 255, 256, 257, 1024, 4096} {
		data := make([]byte, n)
		for {
			for i := range data {
				state ^= state << 13
				state ^= state >> 7
				state ^= state << 17
				data[i] = 32 + byte(state%95)
			}
			if !preferScalarLogLine(string(data)) {
				break
			}
		}
		inputs = append(inputs, "discarded partial line\n"+string(data)+"\n")
		for _, word := range sensitiveWords {
			mixed := []byte(word)
			for i := 0; i < len(mixed); i += 2 {
				if mixed[i] >= 'a' && mixed[i] <= 'z' {
					mixed[i] -= 'a' - 'A'
				}
			}
			for _, control := range []string{"", "\x7f", "\x1b[31m", "\x00", "\u0085"} {
				keyword := string(mixed[:len(mixed)/2]) + control + string(mixed[len(mixed)/2:])
				if len(keyword) > n {
					continue
				}
				for _, at := range []int{0, (n - len(keyword)) / 2, n - len(keyword)} {
					body := append([]byte(nil), data...)
					copy(body[at:], keyword)
					inputs = append(inputs, "discarded partial line\n"+string(body)+"\n")
				}
			}
		}
	}
	return inputs
}

func checkRoutedSanitizerCorpus(t *testing.T) {
	t.Helper()
	inputs := routedSanitizerCorpus()
	for i, input := range inputs {
		for _, truncated := range []bool{false, true} {
			got, gl, gc := sanitizeTail([]byte(input), truncated)
			want, wl, wc := referenceSanitizeTail([]byte(input), truncated)
			if got != want || gl != wl || gc != wc {
				t.Fatalf("routed case%d truncated%v: got %q/%d/%v want %q/%d/%v", i, truncated, got, gl, gc, want, wl, wc)
			}
		}
	}
	t.Logf("%d printable/control/keyword inputs, two truncation modes", len(inputs))
}

func TestSanitizerRoutedEquivalence(t *testing.T) {
	checkRoutedSanitizerCorpus(t)
}
