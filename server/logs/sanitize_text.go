package logs

import "strings"

// needsRuneFilterGeneric matches the character filter in sanitizeTail.
func needsRuneFilterGeneric(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x7f || c < 0x20 && c != '\n' && c != '\t' {
			return true
		}
	}
	return false
}

// preferScalarLogLine selects once per line, before Unicode case conversion or
// the keyword loop. A coincidentally repeated prefix only selects the existing
// implementation; it never changes whether a line is sensitive.
func preferScalarLogLine(s string) bool {
	if len(s) < 16 {
		return true
	}
	if len(s) > 256 {
		return false
	}
	limit := 16
	if len(s)/2 < limit {
		limit = len(s) / 2
	}
nextPeriod:
	for period := 1; period <= limit; period++ {
		for i := period; i < 2*period; i++ {
			if s[i] != s[i-period] {
				continue nextPeriod
			}
		}
		return true
	}
	return false
}

// containsKeywordScalar finishes a false vector candidate on a line of at most
// 128 bytes. The fixed sensitiveWords are short. This bounded scan is not a
// general strings.Contains replacement for long, self-overlapping patterns.
func containsKeywordScalar(s, sub string) bool {
	n := len(sub)
	if n < 2 {
		return strings.Contains(s, sub)
	}
	if n > len(s) {
		return false
	}
	first, second := sub[0], sub[1]
	for i := 0; i <= len(s)-n; i++ {
		if s[i] == first && s[i+1] == second && s[i:i+n] == sub {
			return true
		}
	}
	return false
}

// logCPUHasVector requires an exact XTheadVector token in every ISA record.
// The standard V extension has different encodings and is not sufficient.
func logCPUHasVector(info []byte) bool {
	seen := false
	for _, line := range strings.Split(string(info), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "isa" {
			continue
		}
		seen = true
		found := false
		for _, ext := range strings.Split(strings.TrimSpace(value), "_") {
			if ext == "xtheadvector" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return seen
}
