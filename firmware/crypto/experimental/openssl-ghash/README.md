# OpenSSL C906 GHASH qualification

`0010-ghash-c906.patch` uses the shared C906 five-block GHASH generator under
its BSD-3-Clause license. Four vector lanes and one scalar block process a
prefix; OpenSSL handles the remaining blocks, AAD, padding, lengths and tags.
The vector path starts at 3072 bytes, matching OpenSSL's bulk GHASH chunk.
Short inputs and single-block multiplication retain upstream code.

The expanded key and powers are temporary: there is no context growth or
persistent allocation. On the qualified ABI, `GCM128_CONTEXT` remains 448 bytes
and the AES provider context remains 984 bytes. Allocating 256 EVP contexts
added 324 KiB of anonymous memory in both control and candidate. The large-call
key-expansion and assembly frames total 1376 bytes, plus their callers. CPU
detection uses a one-time 4 KiB line buffer.

Selection currently requires every CPU to identify as C906, advertise
XTheadVector, and permit vectors for the current thread. C906 supplies XTheadBb,
which this kernel does not advertise separately. Missing or inconsistent CPU
information fails closed. `NANOKVM_OPENSSL_GHASH=generic` selects the upstream
primitive; it cannot force unsupported instructions. FIPS and no-assembly
build paths retain upstream code.

## Physical measurements

Three rotating trials on the 1 GHz C906 with video encoding active, OpenSSL
3.5.9 and the project C906/O3 flags. DMA was disabled for the following table.
The control is the **same binary** with `NANOKVM_OPENSSL_GHASH=generic`, so code
placement, providers and allocation layout match. Values are MiB per process
CPU-second, with input offset 5.

| Operation | Bytes | Generic | C906 |
|---|---:|---:|---:|
| GHASH alone | 16384 | 28.580 | 42.728 |
| AES-128-GCM open | 16384 | 9.981 | 10.857 |
| AES-256-GCM open | 16384 | 8.260 | 8.708 |

The unwrapped earlier library measured 23.793 MiB/s for 16-byte GHASH calls
versus 23.205 with the dispatcher (about 2.5% overhead). Across complete small
AEAD records in this run, differences ranged from -1.6% to +8.6%; the generic
same-binary control shows that the positive small-record differences are code
layout effects, not vector GHASH acceleration. No small-record gain is claimed.

`native-medians.csv` includes all sizes, cold-key rows and the earlier full-stack
control (`review`, which also contains the DES/TDES integrations). `qualified`
and `generic` differ only by the GHASH diagnostic option. The measured
libcrypto SHA-256 is
`f07c0c3a40cf92b11a1e14fc7b3f90f975e4e332636f9fef604c1b34fa93c2ce`.

## Verification

- The shared generator passed 200 independent bit-serial reference cases for
  each primitive; all 126 distinct vector encodings matched GNU binutils.
- `test-dispatch.py --negative-control` passed ASan/UBSan CPU, override and
  per-thread permission cases. Removing the thread gate makes the test fail.
- Fresh cross-build, patches applied with zero fuzz.
- On the physical C906, 408 AEAD known-answer/rejected-tag cases plus 2160 GCM
  split/copy/free-original/in-place/tag cases passed with DMA on, DMA off,
  vectors off, generic GHASH, and the device hidden in a private namespace.
  The independent GCM ciphertext/tag fingerprint is `a2d7088437b53472`.
- Installed curl downloaded and verified the same 16 MiB Ubuntu ISO range with
  AES-128/256-GCM over TLS 1.2 and 1.3, and ChaCha20-Poly1305 over TLS 1.3.
  CA verification and the expected SHA-256 were checked. These establish
  integration correctness; an HTTPS throughput improvement is not claimed.

`bench-ghash.c` isolates GHASH through the public low-level AAD API.
`bench-gcm.c NAME SECONDS` measures warm-key AEAD records; `bench-key.c NAME`
measures new keys and a batch of 256 live contexts. They share the existing
independent AEAD harness. Fixed keys/nonces are synthetic benchmark inputs only.
