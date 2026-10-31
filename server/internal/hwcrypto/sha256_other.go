//go:build !linux || !riscv64

package hwcrypto

func openSHAEngine() *shaEngine { return nil }
