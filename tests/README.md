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
I2c transport with mocked Linux ioctls, readiness deadlines and UXC telemetry:

    python3 tests/test-hdmi-sessions.py
    NANOKVM_SANITIZE=1 python3 tests/test-hdmi-sessions.py

They need a C/C++ compiler and no device. The separate
firmware/probes/hdmi-session-runtime.cpp requires exclusive device capture:
stop nanokvm-app before running it. Its capture h264|h265 mode writes
Annex-B video on fd 3; reset checks three recovery cycles; edid TOOL PROFILE
pauses the detector around the programmer and verifies recovered frames.
Keep the original EDID on the host and restore it after a mode-change trial.

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
with `support/sg2002/additional/kvm/src/kvm_i2c.cpp`. It also requires exclusive
ownership with nanokvm-app stopped. `diagnostics` prints a checked UXC register
snapshot; `reset-ready` performs three real reset cycles, counts rejected
admissions before the 250 ms deadline and measures first access and stable
readiness. A failure exits nonzero. It does not change EDID or save device
files. Physical C/D qualification is separate.
