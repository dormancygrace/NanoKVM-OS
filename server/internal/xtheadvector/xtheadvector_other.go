//go:build !(linux && riscv64)

package xtheadvector

func detect() bool { return false }
