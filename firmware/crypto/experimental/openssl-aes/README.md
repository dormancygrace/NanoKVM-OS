# OpenSSL C906 AES qualification

`0005-aes-c906.patch` separately changes the existing scalar AES table lookups
to `th.extu` followed by a scaled indexed load. Byte lookups in the 32-bit Te
tables retain scale 2; the Td4 byte S-box uses scale 0. Tables, schedules and
interfaces stay unchanged. Plain RV64GC and FIPS builds produce byte-identical
AES fallback machine code to upstream.

`0005-aes-dma.patch` adds SG2002 offload for AES-128/192/256 ECB/CBC/CTR and the
CTR primitive inside GCM. OpenSSL keeps padding, streaming, GHASH, tags and TLS.
The FIPS module is excluded. Both patches can be reviewed and qualified
independently; no cryptodev or ENGINE integration is added.

## Native selection and performance

The performance measurements below use OpenSSL 3.5.9, the restricted C906/O3
profile and physical hardware, before the SHA-1/DES/TDES scope cleanup.
`native-measurement-identities.json` pins those binaries, patches, flags and
harnesses. They are historical measurements of the recorded revisions, not
new measurements of the restacked build. Rates are MiB per process CPU-second.
AES assembly, thresholds and provider algorithms are unchanged by the cleanup.

Five rotated primitive trials measure AES-128 encryption/decryption at
0.700/0.703 us per block, versus upstream 1.030/1.030 us. AES-192 and AES-256
also use about 32–33% less CPU per block. Key setup is essentially unchanged.
The earlier indexed-load implementation is additionally compared with the
new extraction/scaled-load sequence in `native-lookup-trials.jsonl`.

Three final rotated EVP trials give:

| Operation | #504 baseline | Patched, DMA off | Patched, DMA on |
| --- | ---: | ---: | ---: |
| AES-128 ECB encrypt, 1 KiB | 13.198 | 16.982 | 17.276 |
| AES-128 CBC encrypt, 1 KiB | 12.046 | 15.103 | 15.149 |
| AES-128 CTR encrypt, 1 KiB | 11.131 | 13.705 | 13.574 |
| AES-128 GCM open, 1400 B | 7.779 | 11.071 | 11.034 |
| AES-128 GCM open, 16 KiB | 8.036 | 11.208 | 14.141 |
| AES-256 GCM open, 16 KiB | 6.695 | 9.462 | 13.620 |

These GCM columns use upstream software GHASH; the separate vector GHASH patch
#527 is not included. A 16 KiB GCM record still reaches the CTR callback as five
3072-byte DMA calls plus a 1024-byte software tail. The complete EVP result
includes that transport cost. The experimental combined-record ioctl and
persistent-descriptor design is not selected by this patch; its earlier
service/wake-lateness measurements do not establish end-to-end SRTP latency.

The full EVP benchmark reinitializes key/IV and finalizes each operation.
Complete 16–128-byte calls can be slower than #504 despite the faster AES
primitive. No unconditional tiny-message improvement is claimed. Raw trials
include these regressions. Assembly, provider dispatch and whole-library
placement effects must not be conflated.

`bench-stream.c` measures repeated EVP updates with an existing key and context,
with DMA disabled. Across all key sizes/directions, the final software path
improves 16-byte updates by 33–38% for ECB, 17–27% for CBC and 21–30% for CTR.
At 64 bytes the corresponding ranges are 43–46%, 31–41% and 31–37%. This is a
streaming-update measurement, distinct from the repeated-key-init benchmark
and from a complete TLS connection. Three rotated trials are in
`native-stream-trials.jsonl`.

## DMA boundaries and tails

Initial minima are 3072 bytes for ECB/CBC and 2048 for CTR/GCM, with a shared
header for both CTR users. Against the selected software path, the smallest
final measured gains across all key sizes and both directions are 8.0%, 17.7%
and 13.2% respectively. `native-threshold-trials.jsonl` retains the three rotated
256–4096-byte sweep with DMA forced from 256 bytes, including the 1024-byte
regressions. That low-threshold DSO is an experiment, not a shipped profile.

After opening the descriptor, AES uses a separate 512-byte continuation
minimum. A 6000-byte operation therefore submits 4096 + 1904 bytes; a 4112-byte
operation submits 4096 and handles 16 bytes in software. In the experiment,
sending every final block to DMA lost up to 3.7% at a 16-byte tail, while a
512-byte tail improved all measured key/mode/direction combinations. The
initial descriptor cost and the cost of another RUN are different, and neither
is zero. The helper accepts AES only; other algorithms are rejected before
opening the device. The legacy provider no longer links the DMA transport.

Provider size checks now skip the DMA helper entirely below the initial
minimum. Request initialization occurs only after successful validation and
descriptor acquisition; the former path zeroed request storage even when
returning immediately to software.

## Correctness and memory

The AES-only ASan/UBSan helper suite passes 92,880 continuation cases plus 960
CBC cases at every block boundary from 4096+16 through 4096+2560 and 25 GCM
counter wrap/fault cases. Seven non-AES IDs must leave input, output and IV
unchanged without opening the device. Negative controls reject full-width
hardware-counter assumptions, repeating the setup minimum on a tail, and
accepting a non-AES algorithm.

`verify.c` retains the 972 AES cases from the former shared DES verifier:
AES-128/192/256 ECB/CBC/CTR, both directions, split updates, context copying,
in-place operation, offsets and output guards. Its independent host OpenSSL
3.5.5 ciphertext fingerprint is `019ff5988780130c`; CI compiles and runs it with
ASan/UBSan alongside the CTR, GCM and tail verifiers. It needs only the default
provider. A wrong-fingerprint negative control is also checked locally.

The earlier native DMA-on/off checks each passed 1944 mixed-algorithm block
cases (including those 972 AES cases), 288 CTR carry cases,
408 AEAD known-answer/rejected-tag cases, 2160 extended GCM cases, 20,736 real
EVP tail cases and upstream AES vectors (313 tests, one FIPS-only skip). Tail
outputs and updated IVs match independent host OpenSSL 3.5.5 fingerprint
`bfe84c43ff49df11`, across all key sizes, modes, directions, offsets and in-place
operation. GCM retains its independent `a2d7088437b53472` fingerprint and tests
context copy/free-original, split updates and rejected tags/ciphertext.

Only completed whole-block prefixes are committed. CBC in-place decryption
preserves the last ciphertext, and IVs advance after success. Every CTR request
splits before its low 32-bit word wraps, even for ordinary 128-bit CTR; software
advances the external counter at the required width between requests. Failed
requests resume software from the last committed prefix.

Provider contexts remain 472 bytes for AES and 984 for AES-GCM, versus 448/960
before DMA key retention. The scalar patch adds no persistent storage and the
DMA helper adds no per-context output buffer. Production retains all 5808
libcrypto dynamic definitions. Native files and raw measurements are published
alongside the standalone benchmark and verification sources.

Four real 16 MiB HTTPS range downloads with the recorded pre-cleanup library pass CA,
status, length and independent SHA-256 checks: AES-128/256-GCM under TLS 1.2
and TLS 1.3. These are qualification runs, not a repeated network-throughput
comparison; see `native-final-https.jsonl`.
