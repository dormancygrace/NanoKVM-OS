# LT6911C/D stock video clocks

C and D automatically use the SG2002 normal video profile; UXC and UE retain the
existing Device Tree profile. Including D is the requested profile policy even
though D was reported unaffected by the C fault.

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

S95 runs the clock helper before either video service starts. For a cached fixed
receiver model c or d, it loads sg2002-video-clocks. For a missing or unknown model,
it first retries S15 detection while both services are stopped. A successful retry
applies the appropriate profile before capture starts; ux and ue keep DT clocks.

If detection still fails, the helper explicitly records deferred policy under
/run/nanokvm and retains DT clocks for that service run. This preserves startup
when the native code's legacy C fallback or later model detection is used.
Identification after service startup does not change live clocks: automatic stock
selection resumes on the next stopped service start. This deferral is permitted
only without a loaded module or an earlier failed profile attempt.

The module uses CCF mux/divider operations; it never retunes a PLL. It verifies
the expected PLL rates, each transition and the final rates. Exclusive parent
rate references protect only the stopped-service transition and are released
before module initialization completes. Child rate exclusivity is also avoided:
CCF recursively protects the parent chain for an exclusive child reference.
The one-Hz fixed-point CAM1PLL representation error is accepted; final divider
rates must match exactly.

Before switching parents, each clock moves to a conservative divider. In
particular AXI must not transiently run at 1080/2 MHz when switching to CAM1PLL.
Startup refuses to apply a profile while either video process is running.
A repeated start verifies the existing model and live rates.

If configuration fails, startup reports the error and management services still
start. Native capture admission checks the matching receiver, module readiness
and live rates before every capture open/reopen. A stock marker alone cannot
waive these checks. Only the explicit unidentified-startup deferral allows the
DT profile with no loaded module; a failed or malformed profile remains blocked.
A partial setup failure can leave lower clocks; reboot restores the boot Device
Tree. Do not unload or reload the module during capture. APK capabilities require
both the helper and the actual module on application-only upgrades.

## Validation and scope

Run python3 tests/test-lt6911cd-clocks.py and repeat with NANOKVM_SANITIZE=1.
The tests execute the production CCF transition and module lifecycle with injected
write, readback, clock lookup, exclusive-reference, parent-rate and rounding
failures. They check reference cleanup on every failure, no persistent PLL
protection after success, live rates, unknown/late model startup for both C and D,
capture admission, degraded startup and package dependencies.

Physical C/D qualification is pending: the local stand has UXC. Reduced clocks
can lower scaling/encoding throughput, especially above 1080p. Including D does
not imply that D has the C fault or that these clocks cause the fault. Issue #63
is not claimed fixed.

The C pinmux reassertion experiment is a separate PR. Periodic polling, CSI PHY
timings and damaged-frame handling are unchanged here, so the two changes can
be evaluated independently on the same source and input mode. Measurements for
the affected C unit must compare the same input and capture workload. Counters
that have stopped because the vendor CIF error handler masked its error IRQs
cannot establish that the link became error-free.
