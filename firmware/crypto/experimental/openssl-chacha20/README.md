# OpenSSL C906 ChaCha20 qualification

Candidate: OpenSSL 3.5.9 LTS with `0001-chacha20-c906.patch`.
Control: PR #504, built with the same source, compiler and C906/O3 flags.
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
that kill syscalls during dynamic loading.

`test-dispatch.py --negative-control` compiles the patched C dispatcher with
mocked hardware/syscall edges under ASan/UBSan. It checks the threshold,
missing capability, all thread-control values, overrides and scalar tails.
Removing either the capability or per-thread gate must fail. The assembly
generator verifies the encoded instructions against GNU binutils. Existing
`../openssl-c906/evp-bench.c 0` checks AEAD known answers and bad tags/ciphertext.

## Measurements

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

The preceding CPU-info-based build passed 1680 native cases and per-thread
checks. Three initial rotated 16 KiB AEAD trials measured about 47.35 versus
29.50 MiB/CPU-s; the current threshold is 640 bytes after a 320-byte dispatch
lost in the initial measurement. Actual TLS 1.2/1.3 ChaCha downloads also passed
file SHA-256 verification. These are prior-build observations: the startup
hwprobe revision and expanded path/overhead tests still need native execution.

The startup-hwprobe revision cross-builds with 5808 unchanged production dynamic
definitions. Host differential checks and dispatch ASan/UBSan/negative controls
pass. The counter is present only in the instrumented library. Native results
will be recorded against each binary's SHA-256 before selecting a new threshold.
