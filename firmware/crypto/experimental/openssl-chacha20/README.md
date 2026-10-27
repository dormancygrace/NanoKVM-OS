# OpenSSL C906 ChaCha20 qualification

Candidate: OpenSSL 3.5.9 LTS with `0001-chacha20-c906.patch`.
Control: PR #504, built with the same source, compiler and C906/O3 flags.
Acceleration requires `NANOKVM_OPENSSL_CHACHA20=auto`; the image downloader sets
this only for its curl child, preserving explicit operator overrides.
Measurements from Go or the kernel do not replace OpenSSL measurements.

## Correctness and dispatch

`verify.c` compares raw EVP ChaCha20 against an independent RFC 8439 scalar
construction: 1680 cases cover offsets 0–15, block/dispatch boundaries through
64 KiB, 32/64-bit counter rollover, split updates, in-place output, guards and
context copies. `--native` also tests concurrent vector-off/on threads.
`--native --hits` requires a library built with `NKOS_CHACHA_TEST` and its private
`nkos_chacha_test_hits` export. It asserts actual assembly entries for eligible
calls and zero entries below 640 bytes, in disabled threads, and with the generic
override. Forked seccomp tests kill file opens/hardware probes after provider
loading, then exercise the first cipher call with `prctl` permitted or denied.
This covers filters installed after library startup, not arbitrary filters
that kill syscalls during dynamic loading. A separate fresh-exec child leaves
the opt-in unset and verifies that a fatal `prctl` rule also survives.

`test-dispatch.py --negative-control` compiles the patched C dispatcher with
mocked hardware/syscall edges under ASan/UBSan. It checks the threshold,
missing capability, all thread-control values, overrides and scalar tails.
Removing the capability, per-thread or explicit opt-in gate must fail. The assembly
generator verifies the encoded instructions against GNU binutils. Existing
`../openssl-c906/evp-bench.c 0` checks AEAD known answers and bad tags/ciphertext.

## Final explicit-opt-in build

The final implementation is `6ce28df8`, libcrypto SHA-256
`6af139e8a8dee9e88e36b7e09a6e5a5ca3d4b001f36da4c8ab14151e1581ca5d`.
It passed the native differential, actual-entry, thread-state and strengthened
fatal-syscall tests described above, plus 408 AEAD known-answer/rejected-tag cases.
The first eligible call makes no UID/GID or file probe. Production retains all
5808 dynamic definitions and no test-counter exports.

Six rotated final-build trials are in `native-policy-bench.jsonl` and
`native-policy-https.jsonl`, with identities/limits in `native-policy-validation.json`.
At 16 KiB, AEAD open measures 48.14 versus 29.38 MiB/CPU-s for the same binary's
generic path. The full-build 64-byte result remains slower than base (6.39 versus
6.84); no small-input gain is claimed. The 640-byte minimum is retained.

| Final HTTPS, median of six | C906/O3 base | ChaCha candidate |
| --- | ---: | ---: |
| curl CPU, seconds | 1.478 | 1.252 |
| Transfer, seconds | 4.509 | 4.417 |
| curl peak RSS, KiB | 8228 | 8204 |
| Anonymous RSS, KiB | 2288 | 2288 |

This is **15.3% less curl CPU**. Peak-RSS ranges overlap (8136–8244 versus
8136–8252 KiB), so the small median difference is not evidence of memory savings.
One base sample has 2292 KiB anonymous memory; all other samples have 2288 KiB.
The private runtime libraries reside on tmpfs: smaps reports some swapped
shared mappings, but SwapPss is zero in every sample. All twelve downloads
passed CA, HTTP range, length and SHA-256 checks. Video remained active;
these runs do not establish an FPS improvement.

## Earlier component and threshold measurements

`bench.c` measures ChaCha20, Poly1305 and ChaCha20-Poly1305 through EVP in the
same binary at 64, 128, 256, 319, 320, 512, 640, 768, 1024, 1400, 4096 and
16384 bytes, with offset 5. It records CPU/wall/user/kernel time and peak RSS.
Component times include their own EVP call overhead; their sum is not an exact
breakdown of the AEAD implementation. Keys/nonces are synthetic test inputs.

Run at least five rotated baseline/candidate/generic trials on physical C906.
`bench-overhead.c`, statically linked to the production libcrypto.a, measures
`prctl`, state preparation/cleansing, the scalar primitive, the vector kernel
and tail, the complete wrapper, and first/second vector calls in fresh threads.
It retains stack wiping in all vector kernel measurements.

The following timing tables describe the recorded startup-hwprobe revision
(hashes in the result files), before the explicit opt-in policy correction.
They must not be attributed to a later binary without remeasurement.
That revision was tested on the physical SG2002 C906 at
1 GHz with active video and zero swap usage. The exact library hashes, six
rotated trials, a second six-trial placement control, memory measurements and
overhead samples are in `native-review-results.json`. Production dynamic
exports remain unchanged (5808 definitions); test counters are absent.

All native path proofs passed: 1680 raw differential cases, real assembly entry
counts, vector-off/on threads, generic override, and both post-load seccomp
cases. Independent Poly1305 references passed 55,050 cases for each library,
including unaligned input, split updates, context duplication/free-original,
and protected-page boundaries. Upstream EVP vectors passed 46 Poly1305 and
27 ChaCha20/AEAD tests with no errors or skips.

### Threshold and call costs

The 320-byte threshold control differs from production in exactly two bytes;
code placement is identical. A second comparison used the same executable,
constant loader path, equal-length override values and process-local disabled
ASLR. This makes the below-threshold candidate and generic paths agree and
confirms the full AEAD regression from lowering the threshold.

Medians of six placement-controlled trials, MiB per process CPU second:

| Operation | Bytes | Generic | Threshold 640 | Threshold 320 |
| --- | ---: | ---: | ---: | ---: |
| Raw ChaCha20 | 320 | 34.48 | 34.47 | 47.95 |
| Raw ChaCha20 | 512 | 35.93 | 35.90 | 43.13 |
| AEAD open | 319 | 16.49 | 16.54 | 16.49 |
| AEAD open | 320 | 17.40 | 17.43 | 16.04 |
| AEAD open | 512 | 20.69 | 20.65 | 19.21 |
| AEAD open | 640 | 21.89 | 24.31 | 24.21 |
| AEAD open | 1400 | 25.02 | 30.04 | 29.91 |
| AEAD open | 16384 | 29.52 | 48.26 | 48.34 |

Keep **640 bytes**. The standalone primitive benefits at 320 bytes, but full
AEAD open loses about 8% at 320 and 7% at 512. At 16 KiB the selected path gains
about 63.5% over the same library's generic mode. Small-record speedups are not
claimed: in the separate complete-build comparison, AEAD open at 64 bytes was
6.52 versus 7.28 MiB/CPU-s for the base build. Binary/data placement affects
these small measurements; that comparison must not be presented as a gain.

Six repetitions of the static component benchmark measured `prctl` at a median
609 ns and state preparation/cleansing at 98 ns. At 640 bytes, scalar processing
was 15.90 us, the kernel plus tail 7.57 us, and complete dispatch 8.54 us. At
16 KiB these were 406.28, 193.56 and 194.90 us. These are separate microbenchmarks,
not an additive decomposition of EVP or TLS.

Fresh-thread samples use thread CPU time because this kernel traps user-mode
`rdcycle`. Across 120 threads, the empty timer pair had a 3.98 us median; first
and second 320-byte vector calls including that timer overhead had medians
21.38 and 7.52 us. The first-call cost is therefore recorded explicitly rather
than inferred from warm throughput. No global kernel timing setting was changed.

### Poly1305 comparison requested in review

The CRYPTOGAMS `poly1305-riscv.pl` from Linux 7.3
(SHA-256 `9a75fc04cd61e8fbd7f7e7cafec070158fb2d964d33c1d3da3d028e10a0a1a72`)
was adapted privately to OpenSSL's init/blocks/emit ABI and tested in both its
64-bit and RV64-executed 32-bit arithmetic forms. This is a different
implementation from the earlier Go assembly adapter. The production patch in
this PR still changes only ChaCha20.

Six rotating-order trials, MiB per process CPU second:

| Operation | Bytes | Existing Poly1305 C | CRYPTOGAMS 64 | CRYPTOGAMS 32 |
| --- | ---: | ---: | ---: | ---: |
| Poly1305 MAC | 1400 | 127.03 | 152.18 | 61.50 |
| Poly1305 MAC | 16384 | 142.67 | 188.00 | 66.24 |
| ChaCha20-Poly1305 open | 1400 | 31.19 | 33.00 | 24.80 |
| ChaCha20-Poly1305 open | 16384 | 48.35 | 53.09 | 34.62 |

All variants retain a 248-byte low-level Poly1305 context. Allocating and
initializing 256 EVP MAC contexts increased anonymous memory by exactly 84 KiB
in every one of three rotated runs per variant. RSS ranges overlap and do not
show a reliable memory saving from either assembly variant.

The 64-bit implementation is **not selected** despite its large-record gain.
The official C906 RTL selects a different multiplication sequence when both
operands fit its short-width condition. Wide `mul`/`mulhu` operations involving
Poly1305 key limbs therefore cannot simply be assumed constant-time on C906.
The 32-bit arithmetic variant avoids that particular width-dependent condition
but is substantially slower than the existing C code. This is a qualification
limitation, not a claim of a demonstrated exploit. See
[the multiplier width test and state machine](https://github.com/XUANTIE-RV/openc906/blob/b0c06eb1f8b3bae663bd8b87eac89ff48e68a57f/C906_RTL_FACTORY/gen_rtl/iu/rtl/aq_iu_mul.v#L264-L275).

### HTTPS with the current startup-hwprobe build

`https-probe.go` is the physical-download harness; build it as a submodule of
`NanoKVM-Server` with the project server module as a local replacement. Set
`-runtime-root` to the directory containing `base/` and `candidate/` libraries;
`-profile alpine` uses the installed libraries. Each process checks one bounded
range download and removes its private data and header files.

`native-https-results.jsonl` contains six rotating-order TLS 1.3 downloads each
for installed Alpine, the current C906/O3 base and this ChaCha20 candidate.
The installed curl fetched a 16 MiB range from the same Ubuntu HTTPS peer over
Wi-Fi while video stayed active. Data passed through the production Go SHA-256
path into a private SD file; every response was HTTP 206, had the expected
length and SHA-256, and the file was synced outside the timed interval.

| Metric, median of six | Alpine | C906/O3 base | ChaCha20 candidate |
| --- | ---: | ---: | ---: |
| curl CPU, seconds | 1.945 | 1.493 | 1.244 |
| Transfer, seconds | 5.178 | 4.673 | 4.244 |
| curl peak RSS, KiB | 7692 | 8188 | 8240 |
| curl anonymous RSS, KiB | 2288 | 2288 | 2288 |

The candidate uses 16.7% less curl CPU than the current base build, with
52 KiB more median peak RSS and unchanged anonymous RSS. Network/wall-time
variation and the absence of an encoder-control experiment prevent attributing
an FPS improvement to this change. A separate TLS 1.2 ChaCha20 download also
passed length and SHA-256 verification. No installed device files were replaced.

### Sandbox compatibility and threshold attribution

A private native OpenSSH listener reproduced a fatal pre-authentication seccomp
violation when a separately patched vector GHASH queried `prctl` on a large
AES-GCM packet. Baseline OpenSSL handled the same packet. ChaCha's dispatcher
used the same syscall pattern, so both vector paths require explicit per-client
opt-in. No SSH filter or authentication policy is weakened.

Additional syscall tracing found exactly one vector-control query per eligible
AEAD record and no hidden per-record file/hardware probes. Changing stack
padding did not remove the lower-threshold regression. Calling the measured
ChaCha primitive together with 64-byte key generation and Poly1305 still won
in isolation: the remaining loss depends on the full EVP caller context.
A precise instruction-cache explanation has not been demonstrated. The 640-byte
threshold is an empirical result, not a claim that the full cause is known.

The native SSH test also caught a separate lazy-initialization issue: OpenSSL's
`ossl_safe_getenv` uses UID/GID syscalls on musl, and OpenSSH's preauth filter
kills `getgid`. The C906 wrapper uses musl `secure_getenv` directly, preserving
secure-execution handling without those syscalls. The native seccomp regression
now kills `getgid`/`getegid` as well as file/probe syscalls.
