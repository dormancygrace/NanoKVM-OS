//go:build !linux || !riscv64 || purego

package logs

import "strings"

func sensitiveLine(line string) bool { return sensitiveLineGeneric(line) }
func needsRuneFilter(s string) bool  { return needsRuneFilterGeneric(s) }
func containsEscape(s string) bool   { return strings.IndexByte(s, 0x1b) >= 0 }
