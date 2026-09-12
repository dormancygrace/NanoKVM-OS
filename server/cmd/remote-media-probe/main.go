// remote-media-probe qualifies the read-only NBD transport on a test device.
// It does not mount USB media or write persistent data.
package main

import (
	"NanoKVM-Server/remotemedia"
	"bytes"
	"fmt"
	"os"
	"time"
)

func pattern(offset uint64, length uint32) ([]byte, error) {
	b := make([]byte, length)
	for i := range b {
		b[i] = byte((offset+uint64(i))*37 + 11)
	}
	return b, nil
}
func run() error {
	const size = 16 * 1024 * 1024
	for cycle := 0; cycle < 3; cycle++ {
		d, err := remotemedia.Open(size, pattern)
		if err != nil {
			return err
		}
		err = func() error {
			defer d.Close()
			f, err := os.OpenFile(remotemedia.DevicePath, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			for _, offset := range []int64{0, 512, 1048576 - 512, size - 4096} {
				b := make([]byte, 4096)
				if _, err = f.ReadAt(b, offset); err != nil {
					return err
				}
				want, _ := pattern(uint64(offset), 4096)
				if !bytes.Equal(b, want) {
					return fmt.Errorf("block mismatch at %d", offset)
				}
			}
			if _, err = f.WriteAt(make([]byte, 512), 0); err == nil {
				return fmt.Errorf("read-only write accepted")
			}
			fmt.Printf("cycle %d: boundary reads and write rejection passed\n", cycle+1)
			return nil
		}()
		if err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("NBD attach/read/disconnect/reconnect passed")
}
