# Host tests

This directory holds the repository's host-side tests: the init scripts in
`kvmapp/` and `firmware/alpine/`, the build and packaging scripts in
`scripts/` and `platform/`, the native contracts, and the update tooling. They run on
the build host. They need no device, except where a test says otherwise.

Tests that sit next to the code they exercise stay there:

- Go tests in `server/`;
- C++ tests in `support/sg2002/additional/kvm/tests`;
- the serial service tests in `firmware/services`;
- the EDID generator test in `firmware/probes/edid120`;
- the web tests in `web/tests`;
- the RustDesk tests in `addons/rustdesk`.

## Running

Most tests run with no arguments from the repository root:

    python3 tests/test-platform-build.py
    sh tests/test-usb-profiles.sh

The receiver session tests use fake I2C transfers and deterministic thread
ordering, including the production maintenance/reset and EDID helpers, the real
I2c transport with mocked Linux ioctls, readiness deadlines and UXC telemetry.
They cover short-owner draining, bounded contention, fresh failure history after
maintenance and publication while a real frame lease stays held:

    python3 tests/test-hdmi-sessions.py
    NANOKVM_SANITIZE=1 python3 tests/test-hdmi-sessions.py

They need a C/C++ compiler and no device. The separate
firmware/probes/hdmi-session-runtime.cpp requires exclusive device capture:
stop nanokvm-app before running it. Its capture h264|h265 mode writes
Annex-B video on fd 3; reset checks three recovery cycles; edid TOOL PROFILE
pauses the detector around the programmer and verifies recovered frames.
Keep the original EDID on the host and restore it after a mode-change trial.

The GPIO event tests execute the production transport and detector service
with fault injection for all three receiver models:

    python3 tests/test-hdmi-events.py
    NANOKVM_SANITIZE=1 python3 tests/test-hdmi-events.py

See [LT6911 GPIO events](../docs/LT6911-GPIO.md) for event polarity, fallback
and exclusive runtime qualification with hdmi-events-runtime.cpp.

With a prepared patched Linux source and cpp/dtc/fdtget, the DT qualification
compiles all five board profiles and checks the INT mux/voltage, variant pin
ownership and absence of an INT GPIO hog:

    python3 tests/test-hdmi-events-dt.py --kernel-source /path/to/patched-linux

The platform boot stage also invokes the DT qualification with its prepared
kernel tree, so release-build CI checks resolved properties for every profile.
Event/session tests cover parked VI suppression and the C measurement cost of
recovery versus full diagnostics.

The Makefile groups some of them, for example `make test-wifi-modules test-alpine-network`.
CI (`.github/workflows/ci.yml`, "Script tests") runs the tests that need no arguments and no root.

## Tests with extra requirements

- **Root, on a disposable loop disk:** `test-alpine-firstboot-storage.py [--image IMAGE]`
  (the first-boot data partition). Without `--image` it writes the fresh image
  partition table itself. `--mkfs-exfat` takes a wrapper when the host
  mkfs.exfat lacks the image version's options. The error paths run without
  root in CI: `test-alpine-firstboot-storage-errors.py`.
- **Root, in a disposable network namespace:**
  - `test-dhcp-gateway.py` (CI runs it with `sudo`);
  - `test-mss-clamp.py`;
  - `test-usb-internet-netns.py`;
  - `test-pmtu-netns.sh`.
- **Built or prepared sources, passed as arguments or environment:**
  - the native contract tests (`test-mjpeg-native-contracts.py`, `test-venc-*.py`, `test-vi-yuv-stride.py`, `test-vi-damaged-frames.py`, …);
  - `test-nanokvm-fip.py` and `test-opensbi-reservations.py` (platform images);
  - `test-addon-apk-upgrade.py` and `test-rustdesk-apk.py` (APKs).
  - `test-aic8800-command.py` and `test-aic8800-timeout.py`. Their `--driver`
    is the patched `aic8800_fdrv` source, as `platform/build.sh modules`
    prepares it.

  Each test's `--help` lists what it needs.
- **`test-usb-prefix.py`** takes the S30usbnet script to test: `--script firmware/alpine/compat/S30usbnet`.
- **`test-video-recovery-native.py`** needs `go`; CI runs it in the Go job.
- **`test-aic8800-bt-rx.c`** is a standalone contract of the AIC8800
  Bluetooth RX path:
  `gcc -fsanitize=address,undefined tests/test-aic8800-bt-rx.c && ./a.out`.

## Kernel zstd and zram contracts

`test-zram-zstd.py --source /path/to/patched-linux` compiles the actual kernel
codec and zram backend on a 64-bit little-endian Linux host. It compares empty
and real dictionaries, injects every allocation failure, checks cleanup and
runs compression/decompression concurrently. Set `NANOKVM_SANITIZE=1` for
ASan/UBSan. Release-build CI runs both modes against its prepared kernel source.
The tests do not access a device or change the supplied source tree.

`test-zstd-c906.py` uses the same `--source` argument and sanitizer switch. It
compares the original and patched codecs on generated 4 KiB pages and short
buffers, checks bit-reader boundaries, cross-decodes identical compressed
output, and probes guard pages and 300,000 random/mutated inputs plus truncated streams.

`test-zram-pages.py` extracts the actual page-fill helper and checks all
word-aligned lengths from 0 through 4 KiB, six fill patterns, prefix canaries,
and an inaccessible following page. It accepts the same source/sanitizer options.

`test-zram-streams.py` compiles the actual zcomp frontend with pthread/CPUHP
shims. It checks direction independence, same-direction serialization, nine
allocation-failure points, concurrent calls, teardown waits, and migration to
an online CPU. The real LZ4/zstd backends are additionally qualified on C906.

## Manual tools

`manual/` holds tools that are run by hand and never by CI.

- **`manual/hang-recorder/`** records the last moments before a device hang.
  `heartbeat.sh` prints one `NKHB` line per second, with uptime and
  `/run/nanokvm/now_fps`, to the serial console. `watch-hang.ps1` runs on a
  Windows host attached to that console. It logs every serial line and pings
  the device, and writes `events.jsonl`, so the moment the heartbeat stops
  can be matched against the network.
  `uart-private.jsonl` in the chosen output directory holds the raw console
  text, login prompts included, so treat it as private.

  1. Copy `heartbeat.sh` and `start-heartbeat.sh` to the same device directory.
  2. From the repository root on Windows, start the watcher:

     ```powershell
     .\tests\manual\hang-recorder\watch-hang.ps1 -OutputDirectory DIR -DeviceHost IP -HeartbeatToken TOKEN -Seconds SECONDS
     ```

     Use a new output directory. The serial port defaults to COM3; pass
     `-Port COM4` (for example) to select another port.
  3. In the device directory containing the copied scripts, run
     `sh start-heartbeat.sh ./heartbeat.sh TOKEN SECONDS`.
     Use the same `TOKEN` in both commands: 1–48 letters, digits or hyphens.
     `SECONDS` must be between 5 and 3600.

  `-AllowSysRq` and `-AutoDumpOnLoss` send Magic SysRq keys. These options
  need a diagnostic kernel: the release kernel is built without
  `CONFIG_MAGIC_SYSRQ`.

### Split application packages

Run `python3 tests/test-split-app-packages.py --apk /path/to/native/apk` with
apk-tools 3. It uses disposable roots and fixture files with the production
recipes' dependencies, replaces and provides metadata. It checks migration from
the combined package, file ownership, preservation of modified settings, fresh
installation, independent web upgrades and rejection of an incompatible API.
External Alpine libraries are represented by a fixture provider; service restart
behavior is tested separately in `server/cmd/nkos-apply-updates`. CI runs the
APK migration test inside an Alpine container.

### ECDSA release signing

`test-ecdsa-packages.py` runs in the Alpine 3.24 CI container with `alpine-sdk`,
Python 3, OpenSSL and coreutils. It builds all thirteen APK recipes with small
synthetic payloads and temporary keys, verifies signatures directly and through
apk, rejects tampering/wrong trust, and exercises RSA-to-EC upgrades with both
index formats. No production key or device is used; package scripts are retained
but not executed in the temporary installation root.


The second receiver probe, `firmware/probes/hdmi-receiver-runtime.cpp`, links
only the I2C transport. Stop nanokvm-app and acquire exclusive device ownership
before running it. `diagnostics c|d|ux` checks the selected model's hardware ID
and prints its available telemetry. D internal identity is available only in
this exclusive probe and is cached once per reset generation, including failed
attempts. A failed ID/cleanup aborts the probe without retrying live control. `reset-ready c|d|ux` runs three reset cycles,
checking that no receiver I2C happens before that model's minimum quiet window.
Omitting the model retains the earlier UXC command syntax. C/D physical
qualification is pending; host tests and a successful cross-build are not a
substitute.

`test-hdmi-sessions.py` exercises the actual production poll/reset/maintenance
functions using a fake clock and controlled transport faults. It covers the
2500/2500/250 ms C/D/UXC reset deadlines, the 250 ms maintenance gap without
shortening pending reset readiness, separate silicon ID versus firmware bytes,
C HDMI/CSI and D telemetry, mixed timing rejection, every session IO/cleanup
failure, and capture isolation from optional diagnostics. The EDID C harness
also checks normalized model-specific sleeps, EINTR and reset failures.
`test-hdmi-receiver-start.py` verifies the startup delay before service probes.

The D live-poll regression injects the reviewed operation-6 failure, then runs
100 healthy stale-dimension polls without an IRQ, plus IRQ/resume/reset
transitions. It requires zero D writes and no internal E1 identity reads.
Exclusive-probe tests cover per-generation identity caching and each ID/cleanup
fault, including no further IO after a failed attempt in the same generation.

The [C906 memory test](manual/c906-memory/README.md) validates the running kernel’s
memmove assembly on SG2002, including overlapping buffers, return values and
unmapped page boundaries.

The [C906 usercopy test](manual/c906-usercopy/README.md) validates the real raw,
SUM-enabled and public usercopy paths, fault recovery, and pipe/socket timing.
