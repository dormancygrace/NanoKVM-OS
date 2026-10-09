//go:build !riscv64 || purego

package sha1mb

var kernel kernelFunc

func vector() bool { return false }

func haveVector() bool { return false }
