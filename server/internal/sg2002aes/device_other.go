//go:build !linux || !riscv64

package sg2002aes

func Open(onError func(error)) (*Device, error) {
	return nil, ErrUnavailable
}
