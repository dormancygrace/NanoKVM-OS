# SG2002 direct transport qualification

This layer uses `/dev/sg2002-aes-probe` and the private version-1 ioctl ABI from
`platform/modules/hwcrypto/hwcrypto.h`. It does not use `/dev/crypto`,
cryptodev, ENGINE or a new kernel module. The transport supports all seven
advertised algorithms; each algorithm's OpenSSL integration and measured size
threshold belongs in its own patch/PR.

Every operation owns a close-on-exec descriptor. Capabilities and ABI are
cached after a successful probe; concurrent first probes may repeat INFO. No
userspace lock, shared request/key/output, or descriptor cache crosses fork,
provider unload or threads. Requests remain bounded to the driver's 4 KiB
maximum. Callers split large operations and retain the original software
state until success. Cipher/Base64 output is committed from private scratch;
hashes do not allocate that scratch. An interrupted or failed final copy
cannot corrupt in-place input before software fallback. Controller, availability, permission and protocol failures
are latched for the library image, while interrupted/resource failures allow
later operations. Libcrypto and the legacy DSO each own a separate cache.
A latched absence or permission error is retried after process restart. `NANOKVM_OPENSSL_DMA=off` is a diagnostic software-only control.

Run `python3 firmware/crypto/experimental/openssl-hwcrypto/test-transport.py`.
The host ASan/UBSan test injects 77 submissions across all seven algorithms:
success, EINTR, EIO, a late EFAULT after writes to private scratch, invalid
completion status and oversized output. It checks unchanged caller state and
in-place data on failure, retry/disable policy and descriptor lifecycle.
It also covers permission failure, non-device paths, incompatible ABI and
poisoned-controller capability reports. These are transport fault tests;
physical hardware correctness and profitability require native measurements.

The transport uses public libc `secure_getenv`; the legacy DSO has no private
libcrypto environment-symbol dependency. Positive capability caching removes
the repeated INFO ioctl and its driver mutex wait. Negative caching covers
ENOENT/EACCES and incompatible requests, while EMFILE/EINTR remain retryable.
Descriptors remain operation-owned, so no descriptor cache or new userspace
lock crosses fork or provider unload. RUN errors still detect a replacement
or poisoned driver and preserve software fallback.

Native integration was rechecked on the restricted-ISA, secure-environment
stack: DMA on/off and software fallback pass 864 SHA, 288 CTR, 408 AEAD and
2160 extended GCM cases per mode. A private OpenSSH preauth sandbox exercises
permission-denied device access without killing the process. Algorithm-specific
throughput and context-size measurements remain in the corresponding reports.

## Native session overhead

`native-session-cost.jsonl` contains three 10,000-iteration runs on the physical
C906 at 1 GHz with video active. The benchmark statically links the matching
libcrypto from stack `0a1b4f68` (including SHA-256 integration); it measures
process CPU time, with provider fetch/warm-up outside repeated timings.

| Operation | Median CPU time per call |
| --- | ---: |
| Empty loop | 0.008 us |
| Device open + close | 17.108 us |
| Device open + fstat + close | 24.230 us |
| Cached-capability begin + end | 25.756 us |
| 4 KiB SHA-256 RUN, descriptor already held | 22.532 us |
| Full 4 KiB EVP SHA-256, reused fetched method/context | 96.627 us |
| Full 4 KiB EVP SHA-256, legacy method lookup each init | 118.715 us |

The first cold begin/end costs 103–114 us of CPU and includes INFO. A separate
one-hash trace confirms one device open, fstat, INFO, RUN and close; a second
open/close reads OpenSSL configuration. Its digest matches an independent
SHA-256 calculation. The raw trace uses `trace-session.c`, a ptrace tracer of
its own child. Its per-syscall wall times include tracer scheduling and are
**not** native syscall latencies; use the untraced loop for the cost estimate.

Descriptor acquisition is substantial (about 27% of this full fetched-EVP
measurement), but these separate loops are not an additive cycle profile.
A permanent library-wide descriptor is not enabled by this patch: it would
pin the module, persist across fork and privilege/sandbox transitions, and
require a defined close/invalidation policy. Keeping operation-owned
descriptors makes the measured algorithm thresholds include this real cost.
Provider lookup is also material; applications can reuse fetched methods
without adding a descriptor cache.

Reproduce by compiling `bench-session.c` with the OpenSSL source/include
directories and its matching `libcrypto.a`, plus `-ldl -pthread`, using the same
C906/O3 toolchain flags as that stack. Run without arguments for the repeated
measurements, or `--hash-once` under the provided tracer for the syscall list.
The binary/static-library identities and digest are pinned in
`native-session-validation.json`. No installed library was replaced.
