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

## Hardware evidence and its limits

Before production integration, the same kernels and hybrid algorithm were
tested on the NanoKVM SG2002 C906 at 1 GHz, Linux 7.3.0-nanokvm-os-r3, Go 1.27.3,
GOMAXPROCS=1 over Ethernet 192.168.1.20. The installed server was unchanged.
All values below are sanitizer microbenchmarks, not boot/video latency.

Three processes per variant, ten operations per process, median around 1 MiB:

| Input | Scalar reference (ms/op) | Hybrid prototype (ms/op) |
| --- | ---: | ---: |
| Existing DHCP fixture | 362.331 | 204.431 |
| Repeated a | 825.379 | 805.825 |
| Repeated pxk | 799.062 | 790.773 |
| Repeated paaaaaad | 1002.552 | 990.370 |
| Repeated passwoxd | 1058.619 | 1050.921 |
| Repeated private_kex | 536.781 | 527.986 |

Selection is not free: isolated short-line cases which retained standard search
ranged from -0.17% to +5.07%. The full repeated-log fixtures were 0.73-2.37%
faster. The ordinary DHCP fixture took 43.58% less time. Do not claim zero
overhead for arbitrary short strings or apply these numbers to all Go code.

The CSV files retain all samples from that **pre-integration prototype**.
They are not measurements of the final runtime-gated server binary or the
final #499 scalar baseline. The final scalar DHCP fixture was about 333 ms/op;
362.331 ms/op above belongs to the earlier prototype control and must not be
used to calculate a gain over #499. Final same-binary hardware measurements
remain required before claiming a production speedup.
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
using the same binary to avoid different instruction layouts:

    GOMAXPROCS=1 ./logs.test -test.run='^$' -test.bench='BenchmarkSanitizeMiB$|BenchmarkSanitizeRepetitive|BenchmarkSensitiveInputs/(16|32|64|95|128)/regular$' -test.benchtime=10x -test.cpu=1
    NANOKVM_LOG_TEXT=generic GOMAXPROCS=1 ./logs.test -test.run='^$' -test.bench='BenchmarkSanitizeMiB$|BenchmarkSanitizeRepetitive|BenchmarkSensitiveInputs/(16|32|64|95|128)/regular$' -test.benchtime=10x -test.cpu=1

Also compare against the separately built PR499 baseline to include the cost of
feature dispatch and line selection.
