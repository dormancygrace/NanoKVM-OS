# OpenSSL 3.5 LTS on the C906

Measured on 2026-10-26 on a NanoKVM with one C906 at 1 GHz, 240 MiB RAM,
Linux 7.3-r3, active HDMI encoding and the installed Alpine curl 8.22.0.
All performance measurements below ran on the physical device.

The measured candidate at `0c78b2ca` uses unmodified OpenSSL **3.5.9 LTS**, project GCC
16.2.0 and the shared C906 profile with `-O3`. Alpine's installed 3.5.9-r0
was built with GCC 15.2 and `-Os`. This comparison measures the complete
build profile, including the compiler version; it does not isolate `-O3`.
The 3.5 series is [supported through 8 April 2030](https://openssl-library.org/source/).

No custom cryptographic assembly or CryptoDMA integration is added to OpenSSL.
Upstream assembly/CPU dispatch and Alpine's feature choices are retained.
Loop and SLP vectorization are disabled. The measured scalar build uses
`-mstringop-strategy=scalar`; the separate experiment changes only that option
to `auto`. This affects compiler memory operations, including some `memset`
expansions, and does not replace libc's `memcpy`.

The current production profile also removes `xtheadvector` from `-march`,
matching the zlib-ng library policy. The recorded physical timings retain the
original profile and are not measurements of this later change. The rebuilt
profile passed 408 AEAD cases under C906 emulation, including a failing
corrupted-answer control, and retains all 5,809 libcrypto and 609 libssl
exported symbols/version nodes from Alpine. These are correctness and ABI
checks, not replacement performance measurements.

## EVP operations

Five rotating-order trials per build, 0.4 process CPU seconds per cell.
Each trial first checks 408 AEAD cases (known answers, boundaries, offsets,
split updates, guards and rejected modified tags/ciphertext). Buffers for
AEAD measurements have offset 5; records have a 5-byte AAD and 16-byte tag.
The table gives medians in MiB per process CPU second, not network throughput.
The full 270 measurements are in `evp-results.jsonl`.

| Operation, 16 KiB | Alpine | C906/O3 scalar | C906/O3 auto |
| --- | ---: | ---: | ---: |
| ChaCha20-Poly1305 open | 16.83 | 29.52 | 29.37 |
| AES-128-GCM open | 7.24 | 8.07 | 8.06 |
| AES-256-GCM open | 6.13 | 6.69 | 6.68 |
| SHA-256 software | 12.69 | 19.89 | 19.86 |
| SHA-512 software | 21.37 | 33.90 | 34.00 |

ChaCha open improves 75.4% over Alpine. At 1400 bytes, scalar gives 25.82
and auto 24.40 MiB/CPU-s (5.5% lower measured median; compiler/code placement
also changes). The auto variant improves
some short digest cases, including SHA-1, but does not improve the measured
TLS workload.

## HTTPS with file hashing and SD writes

The same installed curl downloads the first 16 MiB of the Ubuntu 26.04.1
server ISO over wlan0, with the same server IP and verified certificate.
Every transfer negotiated TLS 1.3, ChaCha20-Poly1305, X25519 and RSASSA-PSS,
returned HTTP 206 and matched SHA-256
`c250fa67736b8e0fc51dbbfb968d5f81a90656f59e9371b9b5fc7bf7a1d54eb2`.
A static Go helper implements the existing pipe -> SD + SHA-256 path using
an unmodified copy of the server's SHA helper, including its existing
CryptoDMA support. This is a download-path probe, not a measurement of all
work performed by the production server. Only curl's dynamic OpenSSL runtime
changes between profiles. It runs at nice 10; the helper uses GOMAXPROCS=1.

One warmup and five rotating-order trials per build, no extra output buffer.
Wall time and CPU include connection setup and transfer, and exclude final
file fsync and the final SHA tail. Peak curl RSS comes from getrusage;
a streaming smaps_rollup sample is taken after 8 MiB. Raw trials, including
warmups (`run=0`), are in `https-results.jsonl`.

| Median, 16 MiB | Alpine | C906/O3 scalar | C906/O3 auto |
| --- | ---: | ---: | ---: |
| curl CPU, user + system, s | 1.946 | 1.488 | 1.492 |
| SHA/writer helper CPU, s | 0.866 | 0.806 | 0.814 |
| Elapsed, s | 5.411 | 4.799 | 4.731 |
| curl peak RSS, KiB | 7612 | 8204 | 8228 |
| curl anonymous RSS at 8 MiB, KiB | 2288 | 2288 | 2288 |
| Encoder FPS | 55.21 | 54.05 | 54.75 |

The scalar candidate uses **23.5% less curl CPU**, finishes this transfer
11.3% sooner and uses **592 KiB more peak RSS**. RSS includes resident code
pages; this is not 592 KiB of extra heap. Anonymous RSS was 2288 KiB in all
baseline trials and 2288-2292 KiB in both candidates. The stripped crypto
and SSL libraries together grow by 634952 bytes. PSS is not an appropriate
comparison here: the installed libraries are shared with other processes,
while the experimental copies live in a private tmpfs directory.

There is **no demonstrated encoder FPS improvement**. Uncapped FPS ranges
were 53.78-56.62 (Alpine), 52.30-57.32 (scalar), 51.48-56.52 (auto).
A separate three-trial, rotating-order control capped curl at 2.5 MiB/s:

| Median at 2.5 MiB/s cap | Alpine | Scalar | Auto |
| --- | ---: | ---: | ---: |
| curl CPU, s | 1.993 | 1.533 | 1.541 |
| Elapsed, s | 7.195 | 7.115 | 7.131 |
| Encoder FPS | 57.85 | 56.64 | 56.65 |

All capped trials are retained in `https-rate-results.jsonl`, including an
Alpine transfer delayed to 9.02 seconds. A rate cap does not make Wi-Fi,
SD writes or per-record bursts identical. The CPU saving persists, but the
video results do not support claiming a video-latency or FPS benefit.

## Separate inline memcpy measurement

`memcpy-bench.c` compiles the same constant-size copies with scalar and auto
strategies into separate objects. Both use the same C906/O3 flags otherwise,
with no LTO and no inline call elimination. Disassembly has 0 XTheadVector
instructions in the scalar object and 24 in the auto object. Both inline the
copies instead of calling libc. The harness checks 576 length/offset/guard
combinations before measuring three trials per cell, at least 0.12 process
CPU seconds per trial. Source and destination are hot buffers; both have
either offset 0 from 64-byte alignment or offset 1. This is not an SD or
large cold-memory bandwidth test. Both strategies share one process, so this
microbenchmark alone cannot isolate vector context-switch overhead.

| Bytes | Auto vs scalar, aligned | Auto vs scalar, offset 1 |
| ---: | ---: | ---: |
| 16 | -6.0% | -4.9% |
| 32 | -26.3% | +0.4% |
| 64 | -38.6% | -4.7% |
| 128 | -16.2% | +28.9% |
| 256 | -20.2% | +28.3% |
| 512 | -19.8% | +33.1% |
| 1024 | -21.3% | +33.2% |
| 4096 | -26.7% | +24.5% |
| 16384 | -30.6% | +20.0% |

The 16-byte functions have identical scalar instructions in both objects;
their 5-6% difference exposes placement/order sensitivity in this small
microbenchmark. Do not attribute that row to vectors or interpret small
differences as an isolated instruction effect. Larger aligned-copy differences
are substantially larger, but these measurements are not a general memcpy
replacement policy.

`memcpy-results.jsonl` retains all 108 measured cells. In the full auto
OpenSSL build, generated vector loads/stores are present in C objects such
as ssl_lib and tls_common; the corresponding scalar objects contain none.
The production selection follows the device results: vector inline copies
are not a general win, and the full auto build gives no measured HTTPS CPU
benefit. They remain a separate experiment.

## Validation and reproduction

`validation.json` records exact build flags, source/runtime hashes, ABI
comparison, generated-vector examples and correctness results. Both clean
scalar builds produced identical bytes for all 13 runtime files. All Alpine
exported dynamic symbols, including symbol versions, are retained. The
common AEAD suite passes on all three profiles, and a deliberately corrupted
known answer fails on each. Upstream ChaCha, AES and SHA suites pass on both
candidates; EC, X25519/X448, HKDF and TLS 1.3 KDF suites also pass on the
production candidate. Configured-out/provider-specific skips are recorded.

The signed APK transition test checks Alpine -> NanoKVM -> Alpine, existing
curl/SSH/apk dependencies and modified user configuration. A contradictory
rollback that still requires nanokvm-openssl is rejected without changing
the installed database, world or configuration. The signed release-channel
test covers all 14 core packages and interrupted rollback recovery.

Build the runtime with the normal `platform/build.sh tools` stage, or directly:

```sh
python3 scripts/build-openssl.py --archive "$CACHE/openssl-3.5.9.tar.gz" \
  --output "$OUT/openssl" --cross-prefix "$CROSS" --jobs 8
```

`OUT` must be a task build directory outside the source tree. The runtime
output directory must not already exist. Keep all experiments isolated from
installed libraries, and coordinate device ownership before running them.
Build the separate auto experiment with `build-auto.py` and the same options.
It deliberately restores `xtheadvector` in addition to selecting auto stringops.
To reproduce the recorded scalar/auto pair, use revision `0c78b2ca`; the current
production profile has a narrower baseline ISA.

To build the EVP probe, point `OPENSSL_BUILD` at the extracted/configured
source tree inside that output, and `BENCH` at this directory:

```sh
"${CROSS}gcc" $(python3 scripts/nanokvm_cpu_profile.py userspace) \
  -mstringop-strategy=scalar -I"$OPENSSL_BUILD/include" \
  "$BENCH/evp-bench.c" -L"$OPENSSL_BUILD" -lcrypto -o "$OUT/evp-bench"
```

Run `evp-bench 0` for correctness only and `evp-bench 0.4` for measurements.
Set `OPENSSL_CONF=/dev/null` and the candidate's `LD_LIBRARY_PATH` and
`OPENSSL_MODULES` for candidate trials; unset those paths for the baseline.
Compile a negative control with `-DCORRUPT_KAT`; it must exit 2.

For the historical memcpy experiment, use the `openssl` profile flags from
revision `0c78b2ca`, which still included `xtheadvector`.
Compile twice with `-DCOPY_IMPL`: scalar uses `-DPREFIX=scalar_`; auto uses
`-DPREFIX=auto_` and replaces the scalar stringop option with `auto`. Link
both objects with the same source compiled without COPY_IMPL. Inspect both
objects with `${CROSS}objdump -d` before running the resulting binary.

The HTTPS helper is a separate static Go module named `loadprobe`. Put
`curl-probe.go` at its root and the four non-test `sha256*.go` files from
`server/internal/hwcrypto` in its `sha/` directory. Build with
`CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build`. On the device it expects
candidate runtimes in `/tmp/nkos-load-audit/c906-o3` and `c906-auto`; run with
`GOMAXPROCS=1` and `-profile baseline`, `c906-o3` or `c906-auto`. The optional
`-rate 2621440` enables the matched rate-cap control. It creates one private
16 MiB file on /data and removes it on exit. Remove the temporary runtime
and helpers when finished; never install them over the live system for this
comparison.
