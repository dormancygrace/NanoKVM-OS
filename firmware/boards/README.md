# SG2002 automatic first-boot profiles

The full image initially boots `sg2002-nanokvm-detect.dts`. Before SysV services
start, initramfs mounts rootfs and invokes `nkos-board-select`. A positively
identified OLED selects an electrical profile. No responding OLED selects a
restricted base/Lite profile, not a claim about a commercial model. Bus errors
and contradictory responses stop selection instead of guessing.

`sensor-defaults/` holds the board-default `sensor_cfg.ini` variants. The
firmware package installs them in `/usr/share/nanokvm/board-defaults`, and
`S02config` seeds `/mnt/data/sensor_cfg.ini` from the LT variant when it is
missing.

| Probe | Selected DT | Native compatibility name | ATX |
| --- | --- | --- | --- |
| I2C1:0x3d | alpha | alpha | Alpha mapping |
| A15/A27 GPIO I2C:0x3d | beta | beta | Serial Full mapping |
| A15/A27 GPIO I2C:0x3c | pcie | pcie | PCIe mapping |
| Successful probes, neither OLED present | lite | beta | Disabled |

The Alpha test happens first. A27 is Alpha's host reset output, so the beta
bus must never be requested after a positive Alpha result. Detection holds
Wi-Fi off, disables SDIO, enables the Alpha I2C mux and deasserts its OLED reset.
Only after a successful negative Alpha probe does the small GPIO-v2 helper
claim beta OLED reset and its open-drain bus. Internal pull-ups allow an empty
Lite connector to yield a NACK rather than a floating bus. MMIO writes are
restricted to those three mux words and two pull configuration words; the
helper rejects execution outside the detection DT and restores them on exit.

The helper does not infer a board from an I2C command error. An early Full
with a failed identity OLED cannot be reliably distinguished this way from
hardware without that daughterboard. This is not a general discovery scheme
for arbitrary third-party carrier boards.

`nkos-board-select` verifies the selected FIT input, writes `boot.sd.new`, syncs,
renames it to `boot.sd`, syncs and reboots. There is no post-write full-image
readback, backup image or automatic rollback. The selected FIT contains the
same accepted kernel/initramfs and the appropriate DT. Normal init trusts this
DT profile, while `/etc/kvm/hw` retains legacy naming for native application
compatibility. `/etc/kvm/board-profile` controls the server's ATX mapping.

## OLED at runtime

`kvm_system` finds the OLED again on every start, independently of the
first-boot choice:

- The board comes from the DT `sipeed,board-revision`; images without it fall
  back to the first letter of `/etc/kvm/hw`.
- Alpha: I2C1, address 0x3d only. Beta and Lite: the GPIO bus, 0x3d, then
  0x3c. PCIe: the GPIO bus, 0x3c, then 0x3d.
- The presence test is one SMBus receive-byte. It changes nothing on a panel
  that is already showing something.
- The layout follows the board, never the address. Only PCIe uses the 64x32
  window; a module added to a Lite stays 128x64 at either address.
- Without an answer the service runs without an OLED.

`/etc/kvm/oled` overrides this, for example for a module added to a Lite.
The file holds `off`, or lines of `bus=N`, `address=0xNN` and
`layout=wide|narrow`; a given address is the only one tried. A malformed
file is ignored as a whole, including files containing NUL bytes, exceeding
4096 bytes, or failing to read completely.

Known limit: first boot cannot tell a Lite with a 0x3c module from a PCIe
board. No second signal for PCIe is known. Such a Lite gets the PCIe profile
(and its ATX mapping) until the card is reflashed without the module.
Moving the SD card between board revisions requires rerunning first boot by
flashing the full image. Runtime peripherals do not continuously redefine
electrical identity.

## Shared versus revision-specific wiring

The stock Sipeed `init_beta_pcie_hw()` intentionally uses the same serial
Full/PCIe wiring for OLED reset A22, Wi-Fi enable A26 and HDMI reset B3. The
older pre-port DTS's E1/E2 declarations are not evidence of different Cube
wiring. Common DTS retains the proven modern wiring and leaves the PCIe HDD
input mux to the PCIe variant. Alpha disables SDIO and uses its separate OLED
and ATX assignment. Base/Lite retains common video/network support but exposes
no ATX GPIO through the server.

Sources:
- [Sipeed hardware initialization](https://github.com/sipeed/NanoKVM/blob/main/kvmapp/system/init.d/S15kvmhwd)
- [Lite and Full construction](https://wiki.sipeed.com/hardware/en/kvm/NanoKVM/introduction.html)
- Kernel `drivers/pinctrl/sophgo/pinctrl-sg2002.c` for mux/config offsets and
  `pinctrl-cv18xx.c` for pull bits; GPIO controller labels match GPIO-v2.

Host fixtures exercise all selection outcomes, errors, ambiguous responses
and the Alpha no-beta-pin-access invariant. Device acceptance must identify
the exact artifact and board; local PCIe success does not qualify Cube/Lite.
