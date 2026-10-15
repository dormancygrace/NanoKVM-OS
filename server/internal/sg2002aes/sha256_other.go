//go:build !linux || !riscv64

package sg2002aes

func openSHAEngine() *shaEngine { return nil }
