package main

import (
	"encoding/binary"
	"os"
)

const (
	// RTC_ST_ON_REASON and RTC_ST_OFF_REASON, read only (kernel patch 0086).
	bootReasonNvmem = "/sys/bus/nvmem/devices/cv1800-rtc-reason/nvmem"
	// Written once per boot, so a restart of the server does not report the
	// boot again.
	bootReasonMark = "/run/nanokvm-boot-reason"
)

// bootReason is why the chip last came on: "power" (from no power),
// "restart" (a warm reset: a reboot, or a kernel panic), "watchdog" (the
// hardware watchdog bit: Linux stopped feeding it) or "overheat"; "" after the
// first call in a boot, or when the register cannot be read.
func bootReason() string {
	if _, err := os.Stat(bootReasonMark); err == nil {
		return ""
	}
	raw, err := os.ReadFile(bootReasonNvmem)
	_ = os.WriteFile(bootReasonMark, nil, 0o600)
	if err != nil || len(raw) < 4 {
		return ""
	}
	return decodeBootReason(binary.LittleEndian.Uint32(raw))
}

// decodeBootReason reads RTC_ST_ON_REASON: the state machine's last
// transition in the low bits, the inputs that caused it in the high half.
func decodeBootReason(value uint32) string {
	inputs := value >> 16
	switch {
	case value&0xf == 0:
		return "power"
	case inputs&0x800 != 0:
		return "watchdog"
	case inputs&0x200 != 0:
		return "overheat"
	case inputs&0x500 != 0:
		return "restart"
	}
	return ""
}
