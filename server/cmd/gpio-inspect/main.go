// gpio-inspect only reads GPIO chip and line metadata; it never requests lines.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"NanoKVM-Server/internal/gpioio"
	"golang.org/x/sys/unix"
)

func main() {
	paths, err := filepath.Glob("/dev/gpiochip*")
	if err != nil {
		panic(err)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "no GPIO controllers")
		os.Exit(1)
	}
	for _, path := range paths {
		chip, err := gpioio.ChipInfo(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		label := unix.ByteSliceToString(chip.Label[:])
		fmt.Printf("chip=%s label=%s lines=%d\n", path, label, chip.Lines)
		if label != "3020000.gpio" {
			continue
		}
		for _, offset := range []uint32{14, 23, 24, 25, 30} {
			line, err := gpioio.LineInfo(path, offset)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			data, _ := json.Marshal(map[string]any{"offset": offset, "name": unix.ByteSliceToString(line.Name[:]), "consumer": unix.ByteSliceToString(line.Consumer[:]), "flags": line.Flags})
			fmt.Println(string(data))
		}
	}
}
