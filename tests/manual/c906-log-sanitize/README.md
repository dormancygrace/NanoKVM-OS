# C906 log sanitizer measurements

The sanitizer avoids the ANSI regexp when there is no ESC byte and skips
Unicode classification for printable ASCII, LF and TAB. Secret detection,
invalid UTF-8 replacement, PEM handling, output bounds and archived-log
redaction remain enabled.

Measured on the SG2002 C906 at 1GHz, Linux7.3.0-nanokvm-os-r3, Go1.27.3,
GOMAXPROCS=1. Baseline is main c93fb1ad; both binaries use the same added tests.
Three separate processes per implementation, with order base/candidate,
candidate/base, base/candidate. The installed server was unchanged and no
browser video viewer was connected. These are sanitizer microbenchmarks,
not full startup or streaming measurements.

For BenchmarkSanitizeMiB (the existing ~1MiB ASCII DHCP-log fixture), median:
478.435ms → 333.671ms (-30.26% time).
Allocated bytes fall from5570896 to3467704 per operation (~2MiB fewer).
Complete measurements, including64B/1KiB/64KiB/1MiB Unicode and ANSI/control
inputs, are in measurements.csv. The small differences for fallback inputs
are not a claim of a separate speedup.

## Reproduce

Build baseline and candidate RISC-V logs test binaries with the pinned Go:

    GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=0 go test -c -o logs.test ./logs

Use the same test file for both; baseline substitutes the original sanitize.go.
Run each binary three times on the device, alternating their order:

    GOMAXPROCS=1 ./logs.test -test.run='^$' -test.bench='BenchmarkSanitizeMiB$|BenchmarkSanitizeCharacterPaths' -test.benchtime=200ms -test.count=1 -test.cpu=1

Take the median across processes, retaining all results. Long cases exceed
the time budget in one iteration; the CSV records the actual iteration count.

## Validation

- Host: go test -race ./logs ./logger; go vet ./logs ./logger.
- Device: the complete logs test suite passes, including every possible input
  byte, Unicode format/control characters, invalid UTF-8, ANSI/OSC sequences
  and secrets revealed by character filtering.
- Production RISC-V server cross-build passes with the prepared runtime and
  existing six-library native bundle.
- Prior exploratory differential test:10261 inputs in both truncation modes
  matched the original sanitizer on host and C906.

RVV experiments are separate from this scalar Go change.
