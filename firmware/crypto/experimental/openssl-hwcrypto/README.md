# SG2002 direct transport qualification

This layer uses `/dev/nanokvm-hwcrypto` and the private version-1 ioctl ABI from
`platform/modules/hwcrypto/hwcrypto.h`. It does not use `/dev/crypto`,
cryptodev, ENGINE or a new kernel module. The transport supports all seven
advertised algorithms; each algorithm's OpenSSL integration and measured size
threshold belongs in its own patch/PR.

The module ships in the same image as libcrypto, so there is no INFO, ABI,
capability or device-type probe. A process opens the device once
(close-on-exec) and its threads share the descriptor: the driver keeps no
per-open state and serialises every RUN under its own mutex. The owner pid is
kept with the descriptor; a forked child closes the inherited copy and opens
its own, so a sandboxed child (OpenSSH preauth) gets the usual permission
error and the software path, not an inherited root-only descriptor. EBADF or
ENOTTY (the application closed the descriptor, e.g. `closefrom`) forget it
without closing, and the next operation reopens. No userspace lock or shared
request/key/output exists. Requests remain bounded to the driver's 4 KiB
maximum. Callers split large operations and retain the original software
state until success. Out-of-place cipher output goes straight to the caller's
buffer; in-place output is committed from private scratch, so a failed final
copy cannot corrupt the input before software fallback. Hashes use no scratch.
EIO/ETIMEDOUT (engine poisoned until reboot), absence, permission and protocol
errors are latched for the library image; EINTR, EFAULT and EMFILE retry on a
later operation. A latched error is retried after process restart.

Run `python3 firmware/crypto/experimental/openssl-hwcrypto/test-transport.py`.
The host ASan/UBSan test injects 126 submissions across all seven algorithms,
in place and out of place: success, EINTR, EIO, ETIMEDOUT, EINVAL, EBADF and a
late EFAULT after the driver wrote the output. It checks unchanged caller state
and in-place data on failure, retry/disable policy, one open per process,
fork and stale-descriptor handling, and permission/absence failures. Its
negative control sends in-place requests straight to the caller's buffer.
These are transport fault tests; physical hardware correctness and
profitability require native measurements.

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

Descriptor acquisition was substantial (about 27% of this full fetched-EVP
measurement), but these separate loops are not an additive cycle profile.
The transport now keeps one descriptor per process (above): an operation pays
a `getpid` call instead of open, fstat and close (and INFO on first use). 
The thresholds were requalified for the shared descriptor by wall clock
(`openssl speed -elapsed`, stand .20, 2026-11-09, two rounds; plain
`openssl speed` counts user time only and hides the time spent in the driver).
MB/s, software -> DMA:

| Operation | 256 B | 512 B | 1024 B | 1400 B |
|---|---|---|---|---|
| AES-128-CBC | 20 -> 18-21 | 20-21 -> 31-34 | 21 -> 50 | 21 -> 58-60 |
| AES-128-CTR | 19 -> 19-21 | 19 -> 31-35 | 19 -> 52-55 | 19 -> 58 |
| AES-128-GCM | 8.4 -> 5.8-6.5 | 9.2-9.5 -> 9.4-9.8 | 10 -> 13-14 | 10 -> 14 |

SHA-256: 768 B 16 -> 15.5, 1024 B 17 -> 23-25, 2048 B 19 -> 38-44.

AES therefore uses DMA from 512 bytes (ECB/CBC/CTR and the GCM counter
callback, continuation 512) and SHA-256 from 1024 bytes. The descriptor keeps the
module in use while a process holding it runs (`rmmod` needs those processes
stopped), and a process that drops privileges without forking keeps device
access. Provider lookup is also material; applications can reuse fetched
methods.

Reproduce by compiling `bench-session.c` with the OpenSSL source/include
directories and its matching `libcrypto.a`, plus `-ldl -pthread`, using the same
C906/O3 toolchain flags as that stack. Run without arguments for the repeated
measurements, or `--hash-once` under the provided tracer for the syscall list.
The binary/static-library identities and digest are pinned in
`native-session-validation.json`. No installed library was replaced.
