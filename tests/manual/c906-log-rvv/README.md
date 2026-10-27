# C906 log-text RVV qualification

This backend is local to the log sanitizer. It does not replace Go's global
strings package, runtime memory functions, or kernel string routines.

Linux/riscv64 builds use XTheadVector only when every CPU ISA record contains
the exact extension token and PR_RISCV_V_GET_CONTROL reports vector state ON.
Other builds, the purego tag, unavailable vector state, and
NANOKVM_LOG_TEXT=generic use the original Go implementation. The environment
override is read on first use; compare in separate processes.

The old RVV 0.7.1 byte kernels perform ASCII classification, ESC-byte search,
and first/last-byte candidate search for the fixed secret keywords.
They are length-bounded, make no cgo calls and do not load beyond a string.
LMUL follows candidate/input length: m1 through 16 B, m2 through 32 B, m4 through 64 B,
m8 above 64 B. The instruction checker independently assembles every WORD with
GNU as -march=rv64gc_xtheadvector.

Short lines under 16 B use the original sensitiveLine body. Lines up to 256 B also
retain that function if their prefix repeats with a period of 1..16 B. Selection
happens once before case conversion and the keyword loop. A false positive in
the prefix detector only selects the standard implementation. After a false
vector candidate on another line of at most 128 B, a bounded scalar scan finishes
the search. Long lines fall back to strings.Contains.

## Final-head device evidence

An independent [device check of eec43187](https://github.com/dormancygrace/NanoKVM-OS/pull/500#discussion_r4387869446)
compared the production runtime-gated head with the shipped #499 scalar code.
The NanoKVM C906 ran at 1 GHz with the server running and GOMAXPROCS=1. Both
revisions were built with the same prepared Go 1.27.2 toolchain; these are not
measurements of the current Go 1.27.3 build. Default and generic modes used the
same head binary in separate processes. Three rounds rotated the variant order.

| Input | Shipped #499 | Head, generic override | Head, default |
| --- | ---: | ---: | ---: |
| DHCP fixture, about 1 MiB (ms/op) | 351.9 | 355.8 | 202.9 |
| Real device logs, 2.2 MB (seconds, best of five per run) | 1.672 | 1.679 | 1.339 |

These are medians: about 42% less time than #499 on the DHCP fixture and 20%
on this real-log corpus. The corpus covered 17 MaxBytes windows from apk,
dmesg, boot archives and RustDesk logs, with both truncation modes. Only
output digests and timings left the device. All variants produced identical
digests for identical input bytes; a log grew during round three, so changing
inputs were excluded from cross-variant comparisons.

All seven TestLogVector tests executed and passed with default dispatch.
With NANOKVM_LOG_TEXT=generic, six hardware tests skipped and the remaining
test and the whole package passed. The separate #499 package tests passed.

The generic and #499 aggregate timings are close, but this does not establish
zero dispatch cost for arbitrary short strings. Short-line measurements at
-benchtime=10x were too noisy for precise conclusions. Repeated-log fixtures
varied by -0.7% to -5.2%; they are not presented as established speedups.
These results concern log sanitization, not boot/video latency or all Go code.

### Earlier prototype samples

The retained prototype-short-lines.csv and prototype-whole-log.csv contain
pre-integration Go 1.27.3 measurements. Their DHCP control was 362.331 ms/op
and the hybrid prototype was 204.431 ms/op (-43.58%). That control was neither
the final #499 build nor the control used for the final-head results above.
The prototype's repeated-fixture differences of 0.73-2.37% overlap the sample
spread and are not claimed as gains. Its isolated short-line overhead reached
5.07%. Keep these samples as historical evidence, not as the final baseline.

All 12 production assembly kernels were compared with the qualified prototype
as linked Go ABI0 machine code: 912 bytes are identical (kernel-equivalence.json).

## Correctness and build checks

The qualified prototype passed 112640 byte/alignment/length combinations for
all four LMULs, protected-page boundaries, concurrent vector work and SIGURG.
Production tests retain those checks, skip hardware-only tests on unsupported
machines, and add the runtime-gate override, ISA parser, all real keywords,
scalar fallback and full sanitizer equivalence to the PR499 reference.
The reference corpus includes both truncation modes, including invalid
UTF-8, Unicode/control characters, ANSI sequences and hidden secrets.
A separate nonperiodic printable corpus covers 16–4096 byte lines, mixed-case
keywords at the start/middle/end, and DEL, ESC, NUL and U+0085 inside keywords.
On a detected XTheadVector CPU without the generic override, a disabled runtime
gate fails the vector tests instead of silently skipping them.

Host race tests, purego tests and vet pass for logs/logger. RISC-V vet and test
cross-build pass. The complete production RISC-V server builds against the
prepared runtime and six native libraries. All 76 embedded instruction words
match GNU as XTheadVector encoding.

## Reproduce

From server/, using the pinned Go 1.27.3:

    go test -race ./logs ./logger
    go test -tags purego ./logs ./logger
    go vet ./logs ./logger
    GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=0 go test -c -o /path/out/logs.test ./logs

From the repository root, using the current RISC-V binutils:

    python3 tests/manual/c906-log-rvv/check-encodings.py --prefix /path/to/riscv64-buildroot-linux-musl-

On C906, run correctness first. The normal run
must execute (not skip) the TestLogVector tests. The generic override should
skip them and still pass all architecture-neutral tests:

    GOMAXPROCS=1 ./logs.test -test.v
    NANOKVM_LOG_TEXT=generic GOMAXPROCS=1 ./logs.test -test.v

Then run each mode in three separate processes, alternating mode order,
using the same binary to avoid different instruction layouts. For short-line
measurements use a time-based -benchtime=1s rather than ten iterations:

    GOMAXPROCS=1 ./logs.test -test.run='^$' -test.bench='BenchmarkSanitizeMiB$|BenchmarkSanitizeRepetitive|BenchmarkSensitiveInputs/(16|32|64|95|128)/regular$' -test.benchtime=10x -test.cpu=1
    NANOKVM_LOG_TEXT=generic GOMAXPROCS=1 ./logs.test -test.run='^$' -test.bench='BenchmarkSanitizeMiB$|BenchmarkSanitizeRepetitive|BenchmarkSensitiveInputs/(16|32|64|95|128)/regular$' -test.benchtime=10x -test.cpu=1

Also compare against the separately built PR499 baseline to include the cost of
feature dispatch and line selection.
