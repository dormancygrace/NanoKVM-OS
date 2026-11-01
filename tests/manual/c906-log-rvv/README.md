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
