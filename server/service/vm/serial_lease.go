package vm

import "sync"

// One holder per serial device within the server: the console probe, or a
// terminal session. flock and TIOCEXCL on the device keep other processes
// out, but not another goroutine of this one, so the holder is recorded
// here first and released last, after the device is closed and its line
// settings are restored.
var serialPorts = struct {
	sync.Mutex
	open map[string]struct{}
}{open: map[string]struct{}{}}

// claimSerialPort records the caller as the device's holder. The release
// function is called once the device is closed; ok is false while another
// holder has it.
func claimSerialPort(device string) (release func(), ok bool) {
	serialPorts.Lock()
	defer serialPorts.Unlock()
	if _, taken := serialPorts.open[device]; taken {
		return nil, false
	}
	serialPorts.open[device] = struct{}{}
	return func() {
		serialPorts.Lock()
		delete(serialPorts.open, device)
		serialPorts.Unlock()
	}, true
}

func serialPortOpen(device string) bool {
	serialPorts.Lock()
	defer serialPorts.Unlock()
	_, open := serialPorts.open[device]
	return open
}
