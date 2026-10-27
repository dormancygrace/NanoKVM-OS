# C906 Poly1305 qualification results

Measured on the SG2002/C906 NanoKVM stand on 2026-10-25, at 1 GHz with live
video over Ethernet. The change optimizes the Go TLS implementation. The
kernel WireGuard implementation is measured separately and is unchanged.

## Implementation and baseline

Baseline is the already accelerated NanoKVM Go 1.27.3 implementation:
x/crypto v0.57.0 RISC-V Poly1305 assembly with fast native unaligned loads.
The candidate folds r1 products before carrying and schedules the resulting
ten multiplies for the C906. It retains the existing load fallback, message
tail and public API. The original pinned upstream source remains in the tree
as a reference; the preparer installs the new source.

A read-only inspection of the running server found the baseline update
instructions and confirmed its fast-misaligned-load flag was true. This
comparison therefore does not attribute enabling fast loads to the new
arithmetic. No installed server or kernel was replaced during these tests.

For each final Sum, AEAD and TLS binary pair, the complete ELF .text has
468 changed bytes, all inside the same 506-byte Poly1305 update function.
Every other instruction byte and its address is identical between the pair.
Forty unreachable padding bytes deliberately retain the previous function
extent. [provenance.json](provenance.json) records the binary identities.
The final test rebuild changes only an opt-in flag's skip message relative
to the timed Sum/AEAD helpers; the arithmetic is identical.

## Correctness

Both reference and candidate pass the upstream Poly1305,
ChaCha20-Poly1305/XChaCha20-Poly1305, and existing C906 ChaCha20 tests.
The candidate additionally passes:

- 18,704 state/tag comparisons, including a separate big-integer oracle,
  clamped-key extremes, nonzero accumulators, every offset modulo 16 and
  dense short lengths.
- 1,056 read-only/guard-page cases on both fast-load and byte-load paths.
- The existing 8,000 streaming/misalignment checks.
- A negative control that corrupts a captured tag and is rejected.
- Validated payloads through every measured real TLS and WireGuard exchange.

An earlier independent integer model checked 104,320 boundary/random states.
The selected arithmetic has no added secret-dependent branches or addresses;
hardware multiply timing is not asserted to be operand-independent.

## Sum and complete AEAD

The public Sum and AEAD tests cover 27 sizes from zero through 64 KiB,
including 15/16/17, 31/32/33, 63/64/65, 127/128/129, 255/256/257 and
511/512/513. Sum tests offsets zero and five. AEAD tests both Seal and Open
with five-byte associated data and buffers starting at offset five.

Every cell has three repetitions in each of three processes. Report the
median within each process, then the median of those three medians.
Reference/candidate order is AB, BA, AB. ASLR is disabled only for each test
process. Data and key are deterministic and nonzero. CPU is measured with
the current thread's CPU clock; wall time is recorded independently.
[medians.csv](medians.csv) includes every cell and all process medians.

CPU-time change, candidate versus reference (negative is faster):

| Bytes | Full Sum, offset 5 | AEAD Seal | AEAD Open |
| ---: | ---: | ---: | ---: |
| 0 | +0.75% | -0.12% | -0.33% |
| 1 | -2.97% | -1.12% | -0.90% |
| 16 | -2.90% | -0.25% | -1.01% |
| 17 | -3.94% | -0.79% | -1.19% |
| 64 | -9.89% | -2.08% | -1.58% |
| 128 | -14.28% | -2.42% | -2.72% |
| 256 | -16.83% | -4.16% | -4.12% |
| 512 | -20.04% | -4.26% | -4.35% |
| 1400 | -18.46% | -6.05% | -5.46% |
| 4096 | -20.20% | -6.98% | -7.31% |
| 16384 | -21.28% | -7.38% | -7.51% |
| 65536 | -21.04% | -6.99% | -6.91% |

No AEAD cell in this matrix has a positive CPU-time change. At 1–128 bytes,
savings are small, about 0.3–2.7%; at 1400 bytes they are about 5–6%, and at
16 KiB about 7–8%. Zero-length Sum at offset five is +0.75%, retained above.
These are CPU-time reductions, not equal percentages of throughput,
whole-system CPU, video FPS or browser-mounted ISO speed.

## Real TLS 1.2 and TLS 1.3

A separate temporary server process used the actual crypto/tls stack, with
ChaCha20-Poly1305 explicitly verified at both versions. The native Windows
client communicated over Ethernet with 192.168.1.20. It verified an ephemeral
certificate delivered through SSH and checked every payload. The ordinary
HTTPS/video server remained active. Handshakes, command headers and readiness
were outside the timed loops.

The first cohort tested 1, 5, 16, 64, 128, 512, 1400 and 16384 bytes in upload,
download and request/response modes, with three processes per implementation.
It showed substantial network/run variability, including +36.04% server
CPU time for TLS 1.3 sending 1400-byte records. This result is retained in
[tls-medians.csv](tls-medians.csv), rather than discarded.

A predeclared follow-up extended the batches for 5, 128, 1400 and 16384 bytes:
4096 calls for the two short sizes, 2048 at 1400 bytes and 512 at 16 KiB.
It again used three interleaved processes per implementation. That earlier
1400-byte send slowdown did not recur: the longer TLS 1.3 send median was
-6.73% CPU. Other complete-connection CPU medians still vary; for example,
TLS 1.3 at 128 bytes is +4.74% on receive and +5.55% on send, with overlapping
individual-process ranges. A universal end-to-end TLS speedup or absence
of all possible network regressions is not established.

Short request/response latency in the longer cohort:

| TLS | Payload | Reference median RTT | Candidate median RTT | Change |
| --- | ---: | ---: | ---: | ---: |
| 1.2 | 5 B | 0.627 ms | 0.626 ms | -0.27% |
| 1.2 | 128 B | 0.649 ms | 0.644 ms | -0.63% |
| 1.3 | 5 B | 0.626 ms | 0.624 ms | -0.27% |
| 1.3 | 128 B | 0.644 ms | 0.644 ms | +0.03% |

This supports essentially unchanged short-request latency in the measured
setup. The reliable isolated result is the AEAD CPU improvement. Complete
TLS results include TCP, Windows scheduling and simultaneous video work;
they are not estimates of the cipher alone. The two cohorts remain separate
in the CSV, including user/system CPU, wall time, RTT median and p95.
RTT fields are zero placeholders outside request/response mode.

## Current kernel WireGuard

The installed kernel uses RISC-V Cryptogams/OpenSSL Poly1305 assembly,
already folding r1 products, and the existing C906 ChaCha20 implementation.
It does not call the changed Go Poly1305. No candidate kernel A/B gain is
claimed here.

A temporary kernel interface used MTU 1420 and a dedicated peer/address range;
the existing VPN remained active. Its authenticated endpoint was the Windows
Ethernet address. A native Windows wireguard-go netstack client avoided host
VPN or routing changes. The direct control used the Windows TCP/IP stack.
Consequently the comparison includes different host-side stacks as well as
the tunnel, and cannot attribute every latency difference to the C906.

Three direct/tunnel pairs, video active; throughput uses the longer of sender
and receiver elapsed time. Each bulk case transfers and verifies 32 MiB.

| Device operation | Direct Ethernet | Kernel WireGuard | Whole CPU busy, WireGuard |
| --- | ---: | ---: | ---: |
| Receive | 11.31 MiB/s | 4.34 MiB/s | 98.4% |
| Send | 11.26 MiB/s | 6.24 MiB/s | 100.0% |

UDP request/response checks cover payloads 8, 16, 32, 64, 128, 512, 1200 and
1392 bytes, 256 validated exchanges per size and three runs. At 8–128 bytes,
median RTT is 1.02–1.03 ms through WireGuard versus 0.54–0.55 ms directly.
At 1392 bytes it is 1.52 versus 0.73 ms.
[wireguard-medians.csv](wireguard-medians.csv) retains all measurements.

Whole-CPU busy comes from /proc/stat and includes background video, interrupt
and kernel-worker time; process CPU alone would miss WireGuard workers.
This is consistent with a CPU constraint in this setup, but does not identify
the fraction spent in Poly1305. The release kernel has PERF_EVENTS and
function tracing disabled. A kernel profile would require a separate debug
kernel session. The benchmark establishes a baseline for that investigation.

## Build and stand status

Preparation from the pinned Go toolchain, both RISC-V package-test builds,
Linux/Windows TLS helper builds, go vet, and the existing CPU-profile,
platform-build/configuration and kernel-configuration checks pass.
The reference builder rejects an unprepared GOROOT. Kernel configuration
checks reused the existing source/SDK read-only and wrote a task-owned
temporary output directory.

Two setup errors were corrected before their relevant completed series:
the AEAD measurement helper originally did not match the builder's
*_bench_test.go inclusion rule, and WSL direct LAN sockets were unavailable,
so the TLS network client was built for native Windows. A final correctness
upload later exhausted the 64 MiB RAM tmpfs with accumulated test binaries;
completed helpers were removed and the remaining tests passed one at a time.
No incomplete series is pooled into the reported measurements.

The temporary WireGuard interface, test listeners and all RAM executables
were removed. The boot ID and application server PID are unchanged.
The installed server, boot.sd and fip.bin hashes match the pre-test values.
Video continued over Ethernet, USB functions stayed off, and no kernel
oops/BUG appeared. No on-device backup copies were created.
