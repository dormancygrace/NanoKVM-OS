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

The Makefile groups some of them, for example `make test-wifi-modules test-alpine-network`.
CI (`.github/workflows/ci.yml`, "Script tests") runs the tests that need no arguments and no root.

## Tests with extra requirements

- **Root, on a disposable loop disk:** `test-alpine-firstboot-storage.py --image IMAGE`
  (the first-boot data partition).
- **Root, in a disposable network namespace:**
  - `test-dhcp-gateway.py` (CI runs it with `sudo`);
  - `test-mss-clamp.py`;
  - `test-usb-internet-netns.py`;
  - `test-pmtu-netns.sh`.
- **Built or prepared sources, passed as arguments or environment:**
  - the native contract tests (`test-mjpeg-native-contracts.py`, `test-venc-*.py`, `test-vi-yuv-stride.py`, …);
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
