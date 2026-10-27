//go:build ignore

package main

// Windows is used only as the Ethernet client; CPU is measured on Linux.
func cpuTime() (uint64, uint64) {
	panic("the test server requires Linux")
}
