# LT6911 interrupt-driven receiver reads

The production detector uses the Linux GPIO character-device v2 interface.
It finds controller `5021000.gpio` by label and requests input line 1
(PWR_GPIO1 / GPIOE1); the `gpiochipN` number is not fixed. No custom
`/proc/lt_int` kernel driver is required.

Both edges are queued by the kernel; the detector selects the stock polarity:

| Receiver | Edge that requests a receiver read |
| --- | --- |
| LT6911C | Rising |
| LT6911D | Falling |
| LT6911UXC | Falling |

With a working event source, a successful snapshot and no pending recovery,
there are **no periodic receiver reads**, including on C. Reading GPIO events
does not access the receiver over I2C. The existing VI state monitoring and
manual detection modes continue independently. A fresh abnormal VI report in
normal mode requests recovery even if an open GPIO descriptor delivered no edge.

## Admission and recovery

- Subscribe before the initial snapshot, so an already connected source works.
  The existing UXC pre-MMF startup snapshot is retained.
- Coalesce applicable edges, with at least 100 ms between healthy snapshots.
  Keep the edge pending across paused, settling and failed I2C reads.
- Retry failed or reset-delayed reads once per second. A successful ready or
  no-signal snapshot ends recovery; a stable no-signal input waits for an edge.
- Track GPIO request and line sequence numbers. A gap requests a fresh
  diagnostic snapshot, even if the applicable edge was among the lost events.
- If GPIO is missing, busy, unsupported or fails, log the error and use the
  existing one-second I2C fallback. Retry GPIO acquisition every five seconds.
  After reconnecting, take another snapshot before returning to event-only reads.
- Use fresh VI reports (the existing ten-second cadence) as a second recovery
  source. Codes 2–7 (no input frames, CSI size mismatch or unknown CSI failure)
  request one resync snapshot; healthy code 1 and unavailable/uninitialized code
  0 do not. Never replay a stale VI fault every detector iteration. Failed I2C
  recovery retains its normal one-second retry.
- Drain events during manual modes and retain pending work until normal mode.
  Only a confirmed applicable edge permits automatic mode switching.

The common Device Tree selects PWR_GPIO1 function 0 (GPIOE1) at GPIO-controller
probe, using a 1.8 V power domain. Alpha, detect and PCIe retain this group in
their controller pinctrl lists alongside their existing reset/HDD pins; beta
and Lite inherit the common default. This does not hog the GPIO line or set an
output value. The userspace request sets input and both-edge flags only, with
no output, bias, active-low or pad-mux programming. The detector owns the descriptor and closes
it on exit or a transport failure. Reads are nonblocking and bounded even
during an interrupt storm.

The stock counter is in Sipeed's customized
[soph_mipi_rx.ko](https://github.com/sipeed/NanoKVM/blob/509a22f6bd94af85bca264d37f2fab70763b60e3/kvmapp/system/ko/soph_mipi_rx.ko).
Disassembly of that binary shows a rising/falling counter exposed through
`/proc/lt_int`; the application reads and clears it. GPIO v2 supplies queued
events and sequence numbers without that read-clear interface.

The mux value is specified by the SDK's
[PWR_GPIO1 pin definition](https://github.com/milkv-duo/duo-buildroot-sdk-v2/blob/6f8962c394dd0a05729abb089f0feb7d5cc4aa5e/freertos/cvitek/hal/cv181x/config/cv181x_pinlist_swconfig.h#L193).
The existing U-Boot PWR_SEQ1/HDD ownership handoff remains unchanged; it concerns
a different pad. Cube/Lite boot paths have not been physically qualified here.

The VI safety net detects failures visible to VI. A missed edge whose change
leaves VI healthy can still leave optional receiver timing diagnostics stale.
There is no blanket 60-second receiver timer: UXC snapshots also perform
register-service writes, and all three models retain event-only healthy reads.

## Verification and remaining qualification

Run the actual transport and detector-service fault tests without a device:

```sh
python3 tests/test-hdmi-events.py
NANOKVM_SANITIZE=1 python3 tests/test-hdmi-events.py
```

They cover C/D/UXC polarity, zero healthy periodic reads, coalescing, retained
edges, reset/I2C retries, missing/busy GPIO, reconnects, syscall failures,
descriptor zero, cleanup, sequence gaps and wrap, and bounded interrupt storms.
The Python harness also checks the runtime probe's C++ syntax and executes
silent-INT VI recovery cases for all models, including fresh versus stale
reports and retained resync after failed/coalesced reads.

With a prepared patched Linux source and cpp/dtc/fdtget on PATH, compile and
inspect every board DT, including variant overrides and free input ownership:

    python3 tests/test-hdmi-events-dt.py --kernel-source /path/to/patched-linux

The UXC/PCIe stand (mainline 7.3.0-nanokvm-os-r3) was exercised on 2026-10-24
with a RAM-only test library and `firmware/probes/hdmi-events-runtime.cpp`.
Installed services were stopped during exclusive capture and restored afterward.

- A 100-second run checked busy-GPIO fallback, automatic GPIO reacquisition,
  then HDMI 2560x1440 -> 1280x720 -> 2560x1440. Both transitions were detected.
  It produced 5718 encoded frames, one transient capture retry during a
  transition, and no stalls. Quiet periods after GPIO acquisition had no
  receiver-sample trace entries.
- A 15-second run of the initial PR build produced 893 encoded frames, one startup
  retry and no stalls; signal became active and only the initial detector
  snapshot was logged.
- Review recovery test: a test-only preload discarded all eight queued GPIO
  events while the GPIO descriptor stayed active. Both real HDMI transitions
  recovered through fresh VI faults (codes 4 and 3), with no edge-triggered
  samples: 65 seconds, 3493 frames, two capture retries and no stalls.
  The new pinctrl DTBs were compiled/inspected for all five profiles; they
  were not booted on the stand.
- The original source mode, installed application and library hash were
  restored/verified. The GPIO request was released and RAM artifacts removed.

This verifies capture progress and event handling, not visual frame quality.
Physical C and D qualification and a reproduction of Issue #63 remain pending.
The change does not establish that the reported C corruption is fixed.

## Bounded runtime probe

Cross-link `firmware/probes/hdmi-events-runtime.cpp` against the matching
`libkvm.so` and MMF dependencies. Stop `nanokvm-app` first and arrange service
restoration on exit. Run for 10–180 seconds:

```sh
NANOKVM_HDMI_IRQ_TRACE=1 ./hdmi-events-runtime 30
```

The probe enables normal capture admission, never asserts receiver reset,
discards H.265 output, and reports frame/byte/error/stall counts and input size.
It does not flash receiver firmware or write EDID. With tracing enabled,
`reason=initial|edge|resync|retry|fallback` identifies detector snapshots;
the optional vi/fresh fields identify a fresh VI report behind a snapshot.
Normal operation logs only event-source availability changes.
