# MaixCDK components for NanoKVM Enhanced

MaixCDK v4.11.3 (latest stable release checked 2026-09-05) is pinned in
firmware/sources.json. Prepare that revision before building:

```sh
python3 scripts/apply-maixcdk-fixes.py --source /path/to/MaixCDK
```

The helper verifies source hashes and applies both patches without fuzz.
The board-service builder checks that both fixes are present.
The new kvm_system is built with Enhanced GCC 16.2 and its musl/libstdc++.

Required source subset: basic time, errors, logging, filesystem, app state and
system/network helpers; MaixCAM peripheral I2C; bundled inifile2 needed by app
state initialization. The board CMake requirements remain basic/peripheral.
The standalone build uses function/data sections and linker garbage collection,
so unused framework functions are omitted without replacing them with stubs.
YAML/NTP headers are compile inputs for the upstream time source, but their
unused runtime functions/libraries are not needed by the linked service.
OpenCV and Maix vision are not part of this board-service build.

The service links actual Maix timer, IPv4 and I2C write implementations. Legacy
HDMI helper sources are also compiled; unused routines are discarded by the
linker. Capture remains a separately qualified component.

The I2C patch closes descriptor zero, rejects invalid reads/writes, handles a
failed device-directory listing and avoids allocating a read buffer before a
failing address-selection ioctl. Tests use the real patched I2C source with
wrapped syscalls, not a replacement implementation. Host ASan/UBSan and C906
both pass descriptor ownership, invalid arguments, 128 failing address selects,
short read, successful read/write. Physical I2C timing is not tested.

The system patch backports Sipeed's stream-lifetime and empty-runtime-version
fixes. It closes OS-version/device-key streams on a failed read and CPU/NPU
clock streams on every return path. An empty runtime version returns safely.
Run `scripts/build-enhanced-maix-system-probe.py --source /path/to/MaixCDK
--output /path/to/probe` for the host ASan/UBSan contract test using the actual
system and filesystem sources. The recorded build manifests below describe the
earlier candidate; new upstream-fix verification is in
`firmware/evidence/2026-10-01-upstream-fixes/verification.json`.

Board changes: open I2C buses on first use instead of global constructors;
exclude the standalone QR command-line main from the service build; preserve
valid first/last button events and monotonic press duration; return correctly
from thread entry points; bound OLED state indices and reject unknown IP kinds.
IPv4 copying now queries the Maix interface map once per lookup and copies only
the actual string, rather than repeatedly querying for every byte and reading
past short strings. New --version/--self-test options exit before board control.

Build:
```
NANOKVM_MAIXCDK_SOURCE=/path/to/patched/MaixCDK \
NANOKVM_BUILDROOT_OUTPUT=/path/to/enhanced/buildroot-output \
NANOKVM_SYSTEM_OUTPUT=/path/to/system-output \
python3 scripts/build-enhanced-system.py
```
Output: stripped kvm_system, separate kvm_system.debug, link map, dependencies,
source/artifact hashes and licenses. No service installation is performed.
For the syscall contract probe use build-enhanced-maix-probe.py with
NANOKVM_MAIXCDK_SOURCE and NANOKVM_MAIX_PROBE_OUTPUT; set
NANOKVM_BUILDROOT_OUTPUT for C906, otherwise it builds the host sanitizer test.

The stripped service is 113,120 bytes in the recorded build; see runtime-audit
for the authoritative measured size. Dynamic dependencies are the three
Enhanced C/C++ runtime libraries only. Do not interpret file size as RAM usage.

C906 self-test passes with the matching Enhanced loader: timers, Bytes,
errors/logging, actual read-only IPv4 enumeration and board address copying.
No production service replacement or new-kernel boot occurred. Physical OLED,
HDMI bridge ownership, shutdown/signal/thread synchronization, missing-bus
exception handling and remaining warnings need review before installation.
Build uses -Wall/-Wextra and errors for return type/uninitialized data; it does
not claim a warning-free build. Compiler warnings are retained in system-build.txt.
