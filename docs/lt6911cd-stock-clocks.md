# LT6911C/D stock video clocks

C and D use the SG2002 normal video profile; UXC and UE retain the existing
Device Tree profile.

| Clock | C / D | UXC / UE |
| --- | ---: | ---: |
| vc_src0 | 396 MHz | 594 MHz |
| axi_video_codec | 360 MHz | 450 MHz |
| src_vip_sys_1 | 300 MHz | 396 MHz |

The values and parents follow the non-overdrive branch of Sipeed's
[sys_pll_nd](https://github.com/sipeed/LicheeRV-Nano-Build/blob/d4003f15b35d43ad4842f427050ab2bba0114fa5/fsbl/plat/cv181x/platform.c#L376).
This reproduces the source-defined normal profile for these three clocks, not
every clock or PLL in the factory image. The affected C unit's factory clock
registers have not been measured. These are SoC processing clocks, not the HDMI
pixel clock or LT6911 CSI transmitter frequency. CPU, voltage, firmware and EDID
are unchanged.

## Applying the profile

S95 loads sg2002-video-clocks for the cached fixed receiver model c or d,
before either video service starts. The module uses CCF mux/divider operations;
it never retunes a PLL. It verifies the expected PLL rates, each transition and
the final rates, then retains exclusive rate references. The one-Hz fixed-point
CAM1PLL representation error is accepted; final divider rates must match exactly.

Before switching parents, each clock moves to a conservative divider. In
particular AXI must not transiently run at 1080/2 MHz when switching to CAM1PLL.
Startup refuses to apply a profile while either video process is running.
A repeated start verifies the existing model and live rates.

If configuration fails, startup reports the error and management services still
start. Native capture admission refuses C/D capture without a verified matching
profile. A partial setup failure can leave lower clocks; reboot restores the boot
Device Tree. Do not unload or reload the module during capture. APK capabilities
require both the helper and the actual module on application-only upgrades.

## Validation and scope

Run python3 tests/test-lt6911cd-clocks.py and repeat with NANOKVM_SANITIZE=1.
The tests execute the production CCF transition with injected write, readback,
parent-rate and rounding failures, both receiver gates, capture admission,
degraded startup and package dependencies.

Physical C/D qualification is pending: the local stand has UXC. Reduced clocks
can lower scaling/encoding throughput, especially above 1080p. D was reported
unaffected by the C fault; including D is an explicit profile-policy change, not
evidence that D has the same fault. Issue #63 is not claimed fixed.

The C pinmux reassertion experiment is a separate PR. Periodic polling, CSI PHY
timings and damaged-frame handling are unchanged here, so the two changes can
be evaluated independently on the same source and input mode.
