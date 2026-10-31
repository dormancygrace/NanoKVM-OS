//go:build !linux || !riscv64

package hwcrypto

func Open(onError func(error)) (*Device, error) {
	return nil, ErrUnavailable
}
