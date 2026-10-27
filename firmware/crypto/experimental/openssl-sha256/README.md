# OpenSSL SG2002 SHA-224/256 qualification

`0003-sha256-dma.patch` adds the C906 scalar SHA-256 compression adapter and
uses direct CryptoDMA for complete compression batches starting at 4096 bytes.
An opened session also processes a remaining 512–4032 B in hardware; shorter
remainders continue through the scalar compressor. Requests remain capped at 4 KiB.
SHA-224 shares the compressor. OpenSSL retains streaming, length, padding,
initial values and final output. Only completed chunks update chaining state;
absent hardware or partial failure continues in software. FIPS is excluded.
No ENGINE or cryptodev is used, and `SHA256_CTX` remains 112 bytes.

The GNU adapter comes from the separate BSD Go C906 core. It preserves six
callee-saved registers and wipes its 64-byte schedule in a 112-byte frame.
The C906 adapter measured about 10–13% higher compression throughput than
a same-layout control that changes only three dispatch bytes to select the
upstream C compressor. This does not establish a
whole-library small-message gain: the patched 1–128-byte EVP results remained
slower than the separate base library. Small-message speedups are not claimed.

Historical threshold-selection runs measured 4 KiB DMA at 42.80 versus
19.64 MiB/CPU-s upstream. `native-scalar-results.jsonl` and
`native-measurement-identities.json` preserve the matched software comparison's
actual binaries; these timings predate the restricted-ISA/sandbox-policy revision.
The 4096-byte minimum remains a conservative measured choice, not a claim of
fresh timing for every later binary.

Verification: 3072 host ASan/UBSan fault-continuation cases cover offsets,
first/later failures and unchanged buffering/length fields. Final restricted-ISA
native integration passes the independent 864 SHA1/224/256 streaming, copy,
unaligned and boundary cases with DMA on/off, generic and disabled-vector
profiles. Digest fingerprint: `768fd5312ca481be`. The native data flow also
passes real HTTPS downloads with SHA-256 file verification.

`verify.c` checks the digest cases; `bench.c NAME SECONDS` reports CPU, wall
and RSS across input sizes. `NANOKVM_OPENSSL_DMA=off` disables the hardware path.

The fast CI also applies every patch to the SHA-256-verified pinned OpenSSL
archive with zero fuzz, generates its real headers, and executes the actual
`sha_riscv.c`, DMA helper and scalar assembly under QEMU `thead-c906` without
libc. It checks 2096 scalar cases (0–130 blocks, all 16 offsets) and 1344
wrapper/partial-failure cases, including callee-saved registers, the schedule
wipe, context and input guards. Deliberate digest, ABI, wipe and input-advance
faults must fail. This does not substitute for physical DMA qualification.

## Native continuation check

`native-tail-results.jsonl` and `native-tail-validation.json` record five
alternating-order trials of the old and new helpers in the same static binary.
`bench-tail.c` checks real driver output for every 64-byte step from 512 to
4096 B, then compares full compression batches with the scalar result on every
iteration. It asserts the expected number of completed hardware blocks.
The 4 KiB session minimum is unchanged; the added 512 B continuation minimum
is selected from these measurements. The comparison includes the real
operation-owned transport, while excluding EVP setup and whole-library layout
changes. It is not a claim of new full-library throughput.

Reproduction: apply the pinned patch stack with `tests/test-openssl-source.py`;
retain a separately header-generated source tree for the native build. Compile
`bench-tail.c`, the unmodified transport and scalar assembly, and the old/new
`sha256_sg2002.c` renamed to `base_sha256`/`tail_sha256`, with the C906 musl
compiler (`-O3 -static -pthread`, project ISA flags). The baseline is pinned in
`native-tail-validation.json`; the candidate is this patch. Run the resulting
helper on a C906 with the SG2002 crypto device available.

| Compression input | Previous helper, µs | Tail helper, µs | CPU time change |
|---|---:|---:|---:|
| 4096 B | 75.58 | 76.12 | +0.7% |
| 4608 B | 118.15 | 86.60 | -26.7% |
| 5120 B | 139.47 | 89.15 | -36.1% |
| 6144 B | 182.89 | 92.92 | -49.2% |
| 8192 B | 101.78 | 102.34 | +0.6% |
| 8704 B | 143.70 | 112.18 | -21.9% |

Unchanged-path controls fluctuate by a few percent; no gain is claimed for them.
