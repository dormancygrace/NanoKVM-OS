# OpenSSL SG2002 SHA-224/256 qualification

`0003-sha256-dma.patch` adds the C906 scalar SHA-256 compression adapter and
uses direct CryptoDMA for complete compression batches starting at 4096 bytes.
SHA-224 shares the compressor. OpenSSL retains streaming, length, padding,
initial values and final output. Only completed chunks update chaining state;
absent hardware or partial failure continues in software. FIPS is excluded.
No ENGINE or cryptodev is used, and `SHA256_CTX` remains 112 bytes.

The GNU adapter comes from the separate BSD Go C906 core. It preserves six
callee-saved registers and wipes its 64-byte schedule in a 112-byte frame.
A same-layout control replacing three dispatch bytes with the upstream C path
measured about 10–13% better compression throughput. This does not establish a
whole-library small-message gain: the patched 1–128-byte EVP results remained
slower than the separate base library. Small-message speedups are not claimed.

Historical threshold-selection runs measured 4 KiB DMA at 42.80 versus
19.64 MiB/CPU-s upstream. `native-scalar-results.jsonl` and
`native-measurement-identities.json` preserve the matched software comparison's
actual binaries; these timings predate the restricted-ISA/sandbox-policy revision.
The 4096-byte minimum remains a conservative measured choice, not a claim of
fresh timing for every later binary.

Verification: 1920 host ASan/UBSan fault-continuation cases cover offsets,
first/later failures and unchanged buffering/length fields. Final restricted-ISA
native integration passes the independent 864 SHA1/224/256 streaming, copy,
unaligned and boundary cases with DMA on/off, generic and disabled-vector
profiles. Digest fingerprint: `768fd5312ca481be`. The native data flow also
passes real HTTPS downloads with SHA-256 file verification.

`verify.c` checks the digest cases; `bench.c NAME SECONDS` reports CPU, wall
and RSS across input sizes. `NANOKVM_OPENSSL_DMA=off` disables the hardware path.
