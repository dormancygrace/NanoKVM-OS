//go:build linux

package annexb

import (
	"bytes"
	"syscall"
	"testing"
)

// Buffers that end where an inaccessible page begins: a read past the end
// faults instead of passing unnoticed.
func TestIndexGuardPage(t *testing.T) {
	page := syscall.Getpagesize()
	mem, err := syscall.Mmap(-1, 0, 2*page, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Skip(err)
	}
	defer syscall.Munmap(mem)
	if err := syscall.Mprotect(mem[page:], syscall.PROT_NONE); err != nil {
		t.Skip(err)
	}
	usable := mem[:page]
	for n := 0; n <= 400; n++ {
		b := usable[page-n:]
		for i := range b {
			b[i] = 0x55
		}
		check(t, b)
		// Zeros up to the end: a start code would need the byte past it.
		for i := len(b) - 2; i >= 0 && i < len(b); i++ {
			b[i] = 0
		}
		check(t, b)
		if n >= 3 {
			copy(b[n-3:], []byte{0, 0, 1})
			if got := Index(b); got != bytes.Index(b, startCode) || got != n-3 {
				t.Fatalf("n=%d: start code at the end: %d", n, got)
			}
		}
	}
}
