package sha1mb

import "bytes"

// isaHasExtension reports whether cpuinfo (the contents of /proc/cpuinfo)
// has an "isa" line and every one lists ext among its underscore-separated
// extensions.
func isaHasExtension(cpuinfo []byte, ext string) bool {
	found := false
	for line := range bytes.Lines(cpuinfo) {
		name, isa, ok := bytes.Cut(line, []byte(":"))
		if !ok || string(bytes.TrimSpace(name)) != "isa" {
			continue
		}
		has := false
		for tok := range bytes.SplitSeq(bytes.TrimSpace(isa), []byte("_")) {
			if string(tok) == ext {
				has = true
				break
			}
		}
		if !has {
			return false
		}
		found = true
	}
	return found
}
