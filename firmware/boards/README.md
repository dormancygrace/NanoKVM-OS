# SG2002 automatic first-boot profiles

The full image initially boots `sg2002-nanokvm-detect.dts`. Before SysV services
start, initramfs mounts rootfs and invokes `nkos-board-select`. A positively
identified OLED selects an electrical profile. No responding OLED selects a
restricted base/Lite profile, not a claim about a commercial model. Bus errors
and contradictory responses stop selection instead of guessing.

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
