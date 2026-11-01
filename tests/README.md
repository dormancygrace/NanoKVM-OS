# Host tests

This directory holds the repository's host-side tests: the init scripts in
`kvmapp/` and `firmware/alpine/`, the build and packaging scripts in
`scripts/` and `platform/`, the native contracts, and the update tooling. They run on
the build host. They need no device, except where a test says otherwise.

Tests that sit next to the code they exercise stay there:

- Go tests in `server/`;
- C++ tests in `support/additional/kvm/tests`;
- the serial service tests in `firmware/services`;
- the EDID generator test in `firmware/probes/edid120`;
- the web tests in `web/tests`;
- the RustDesk tests in `addons/rustdesk`.

## Running

Most tests run with no arguments from the repository root:

    python3 tests/test-platform-build.py
    python3 tests/test-usb-state.py

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
  - `test-network-multihome.py --netns` (CI runs it with `sudo`; without
    `--netns` it checks the shipped settings only);
  - `test-usb-internet-netns.py`.
- **Built or prepared sources, passed as arguments or environment:**
  - the native contract test `test-mjpeg-native-contracts.py`;
  - `test-nanokvm-fip.py` (platform images).

  Each test's `--help` lists what it needs. The Release build workflow runs
  `test-mjpeg-native-contracts.py` against its patched `native/src/cvi_mpi` and
  `native/src/sensors` after the build, both normally and with ASan/UBSan.
  This includes buffering, pool reclamation, capture parking and injected
  ownership failures; they are required CI gates, not manual-only checks.
- **`test-usb-prefix.py`** takes the S30usbnet script to test: `--script firmware/alpine/compat/S30usbnet`.
- **`test-video-recovery-native.py`** needs `go`; CI runs it in the Go job.

## Kernel zram contracts

`test-zram-pages.py --source /path/to/patched-linux` extracts the actual
page-fill helper and checks all word-aligned lengths from 0 through 4 KiB, six
fill patterns, prefix canaries, and an inaccessible following page, on a 64-bit
little-endian Linux host. Set `NANOKVM_SANITIZE=1` for ASan/UBSan. Release-build
CI runs both modes against its prepared kernel source. The tests do not access a
device or change the supplied source tree.

## Manual tools

`manual/` holds tools that are run by hand and never by CI.

- **`manual/standby/`** is the QEMU standby-mode driver; see its
  [README](manual/standby/README.md).
- **`manual/c906-lz4/`** holds `zram-bench.c`, the zram LZ4 benchmark;
  **`manual/c906-profile-bench/`** holds `c906-profile-bench.c`, an integer
  and float loop timer. Both are cross-built as static executables and run
  on the device; see [c906-lz4](manual/c906-lz4/README.md).

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
Python 3, OpenSSL and coreutils. It builds all nineteen APK recipes with small
synthetic payloads and temporary keys, verifies signatures directly and through
apk, rejects tampering/wrong trust, and exercises RSA-to-EC upgrades with both
index formats. No production key or device is used; package scripts are retained
but not executed in the temporary installation root.

SD storage checks:

- `python3 tests/test-storage-recovery-layout.py`: both recovery guards accept
  legacy and aligned geometry and reject mixed or overlapping layouts.
- `python3 tests/test-alpine-sd-image-boot.py`: MBR offsets, partition payloads,
  FAT hidden-sector field and reading boot.sd at its offset in the full image.
  Set `NANOKVM_TEST_F2FS_TOOLS` to a host f2fs-tools directory to use real
  mkfs/sload/fsck instead of the default lightweight F2FS fixture.
- `sudo python3 tests/test-alpine-firstboot-storage.py`: disposable 2 GiB loop
  disk with aligned geometry; add `--legacy` to exercise the old geometry.
  Both variants verify the second boot preserves data and reflashing handles
  an old exFAT signature. This does not qualify the SG2002 Boot ROM; booting a
  freshly flashed aligned image on hardware is a separate release gate.

For decimal 1 GB first-boot coverage, run the disposable loop test with
`--card-bytes 1000000000`, both with and without `--legacy`. It verifies data
creation, full remaining capacity, preservation on the next boot and reflash.
The non-root error suite also checks the 96 MiB floor and existing installations.

The kernel build also runs `test-lz4-decompressor.py --source <patched-kernel-tree>`
with and without `NANOKVM_SANITIZE=1`. Its constructed blocks cover short overlap
distances, length extensions, partial decoding, external dictionaries, safe and
fast streaming APIs, in-place buffers, truncated input and tight output bounds.

The [C906 memory test](manual/c906-memory/README.md) validates the running kernel’s
memmove assembly on SG2002, including overlapping buffers, return values and
unmapped page boundaries.

The [C906 usercopy test](manual/c906-usercopy/README.md) validates the real raw,
SUM-enabled and public usercopy paths and fault recovery.

### OpenSSL runtime transition

`test-openssl-package.py` uses Alpine 3.24 with alpine-sdk, alpine-keys,
Python, OpenSSL and coreutils. It builds the real recipe, installs stock
RISC-V Alpine packages in a disposable root, upgrades to the NanoKVM
runtime and rolls back to a release that predates it. It verifies library
ownership, curl/SSH/apk dependencies, configuration preservation and atomic
rejection of a contradictory rollback. A signed higher-version Alpine fixture
checks fresh installs and upgrades against the real release dependency;
removing that dependency selects Alpine as a negative control. CI uses fixture payloads;
`--payload PATH` checks a cross-built runtime. No installed host or device
files are changed. Native C906 benchmark sources and recorded results live
in `firmware/crypto/experimental/openssl-c906`.
