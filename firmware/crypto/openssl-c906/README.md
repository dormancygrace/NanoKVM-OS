# NanoKVM OpenSSL patches

Patches apply in filename order to the SHA-256-pinned OpenSSL release in
`platform/sources.lock`, with zero fuzz. The runtime records every patch digest
in `/usr/share/nanokvm/openssl/source.json`. Each algorithm has its own patch
and pull request; upstream upgrades must requalify the affected algorithms.

## ChaCha20

`0001-chacha20-c906.patch` adds our C906 five-block implementation to OpenSSL's
existing RISC-V primitive dispatch. The default provider keeps its existing
ChaCha20/Poly1305, TLS, buffering, tag verification and counter-limit handling.
No ENGINE or cryptodev interface is involved.

The GNU ABI assembly is generated from
`firmware/cpu/go-crypto-c906/gen_chacha_linux.py`, which also supplies Linux's
ChaCha implementation. Its four vector lanes and fifth scalar block share the
round construction with the separate Go generator. The GNU version preserves
the C callee-saved registers, takes a 16-word state and round count, and wipes
its stack keystream. The Go implementation uses a different ABI and buffer
wrapper; its timings are not OpenSSL measurements.

Inputs below the single `OSSL_CHACHA_C906_MIN_BYTES` threshold (320 bytes, one
five-block group; the C glue only hands whole groups to the vector code) use
upstream C code. The stock Zbb/RVV 1.0 kernels are no longer built or
dispatched: the C906 has neither extension.

There is no run-time check: NanoKVM OS runs only on the SG2002, and its kernel
keeps the vector unit on for every user thread
(`platform/kernel/0078-riscv-user-vector-always-on.patch`: no thread can turn
it off or start a program with it off). The ChaCha20, GHASH and Poly1305 paths
therefore make no system call and need no environment variable, so they work
in every process, including those under seccomp filters (OpenSSH's
pre-authentication sandbox). Earlier versions asked `PR_RISCV_V_GET_CONTROL`
on every call and needed the opt-in `NANOKVM_OPENSSL_CHACHA20=auto`, which only
the image downloader's curl set, and a startup `riscv_hwprobe` of the T-Head
vendor key. CI does not run this library: it checks the patches, the
generated assembly and the arithmetic of the C906 kernels in separate test
programs.

`NKOS_CHACHA_TEST` adds an atomic counter at the actual assembly entry and an
accessor for a private test-library export map. Production builds omit both
and retain the original 5808 dynamic definitions.

The assembly is used under its BSD-3-Clause option; the runtime carries the
BSD license alongside OpenSSL's Apache-2.0 license. The new C glue is Apache-2.0.
Generate/verify with the same binutils used to build the firmware:

```sh
python3 firmware/crypto/openssl-c906/generate-chacha.py \
  --binutils /path/to/riscv64-buildroot-linux-musl- --check
```

Qualification sources and results belong to
`firmware/crypto/experimental/openssl-chacha20/`.

## Direct SG2002 transport

`0002-hwcrypto-transport.patch` adds the shared private ioctl client for subsequent
per-algorithm patches. On its own it selects no hardware algorithm and changes
no EVP result. It is compiled only for RISC-V64, outside the FIPS module. The
module ships in the same image, so there is no ABI or capability probe; a
process keeps one descriptor for all threads. EIO/ETIMEDOUT from a poisoned or
hung engine still fall back to software. See
`firmware/crypto/experimental/openssl-hwcrypto/` for fault tests and lifetime rules.

## SHA-224/256

`0003-sha256-dma.patch` uses the shared direct transport for full compression
chunks. OpenSSL owns streaming, padding, digest lengths and software fallback.
The FIPS module is excluded. The remainder goes straight to the scalar C906
kernel; the Zvknh check of the stock dispatch is not compiled for XTheadBb
builds. See `experimental/openssl-sha256` for qualification;
DMA starts at 1 KiB (wall-clock break-even, see `experimental/openssl-hwcrypto`).

## AES

`0005-aes-c906.patch` separately optimizes the existing scalar AES table lookups
with XTheadBb byte extraction and scaled XTheadMemIdx loads. The original
RISC-V/FIPS assembly remains the fallback. It changes neither tables nor key
schedules.

`0005-aes-dma.patch` adds ECB/CBC/CTR and the AES-CTR primitive used by GCM.
OpenSSL retains streaming, padding, GHASH, authentication and TLS handling.
The shared block helper commits only completed chunks and supports separate
full-width and GCM counters. GCM installs the AES-CTR callback for every record;
below the 512-byte DMA minimum it encrypts one AES block per call with 64-bit XOR
and a register counter. See `experimental/openssl-aes` for qualification.

The AES PR is based directly on the SHA-256 patch. SHA-1, DES, TDES and SM4
OpenSSL offloads are not included. The AES helper rejects other algorithm IDs,
and the legacy provider does not link the SG2002 transport. AES block-mode
verification is self-contained in `experimental/openssl-aes/verify.c`.

## Poly1305

OpenSSL 3.5 has no RISC-V Poly1305 assembly, so it ran the generic C code.
`0011-poly1305-riscv.patch` adds `crypto/poly1305/asm/poly1305-riscv.pl`
from CRYPTOGAMS (Andy Polyakov, written for OpenSSL's `poly1305_init`,
`poly1305_blocks`, `poly1305_emit` interface), byte for byte the file Linux
7.3.1 builds for `lib/crypto` (SHA-256 `9a75fc04cd61e8fb...`), so OpenSSL
and the kernel run the same Poly1305. It is used under its BSD-3-Clause
option; the runtime carries `CRYPTOGAMS-BSD-3-Clause.txt`. `build.info`
selects it for `riscv64` like the other architectures' assembly; it is
outside the FIPS module, and the dynamic symbol table is unchanged (5808
definitions). The OpenSSL flags include `-mtune=thead-c906`, for which GCC
defines `__riscv_misaligned_fast`, so the word-load input paths are built
(no byte loads in `poly1305-riscv.o`).

Stand, `openssl speed -evp chacha20-poly1305`, best of three, MB/s:

| | 1400 B | 16 KiB |
| --- | ---: | ---: |
| generic Poly1305, C906 ChaCha20 | 50.8 | 56.8 |
| CRYPTOGAMS Poly1305, C906 ChaCha20 | 54.4 | 62.4 |
| generic Poly1305, generic ChaCha20 | 32.4 | 33.4 |
| CRYPTOGAMS Poly1305, generic ChaCha20 | 33.8 | 34.9 |

From the ChaCha20-only rate of the same build, Poly1305 goes from about 163
to about 219 MB/s at 16 KiB (the kernel's copy measures 228). OpenSSL's
`test_poly1305` and `experimental/openssl-c906/evp-bench.c 0` (408 AEAD
known answers and rejected tags) pass on the device.

### Vector Poly1305

`0012-poly1305-c906.patch` adds the C906 vector Poly1305 that NanoKVM's Go
runtime and kernel use: radix 2^26, four blocks in XTheadVector lanes and
one in scalar registers per group of five. It is
`firmware/cpu/go-crypto-c906/gen_poly1305_vector.py --openssl`, and that
generator executes it against a big-integer Poly1305; see
`firmware/cpu/go-crypto-c906/README.md`.

- **Selection.** As for ChaCha20 and GHASH there is no run-time check (the
  kernel keeps the vector unit on for every user thread, patch 0078):
  `Poly1305_Init` replaces `func.blocks` with `ossl_poly1305_blocks_c906`.
- **Split.** The vector function takes runs of at least eight whole groups
  (640 bytes) with padbit 1. Cryptogams `poly1305_blocks` takes the rest and
  every other call.
- **Context.** The glue converts the Cryptogams context (h0, h1, h2; r0, r1,
  s1) to 26-bit limbs and back, after folding h2 as `poly1305_emit` does.
- **Threshold.** The vector function computes r^2..r^5 on every call, so
  with `EVP_MAC`, a fresh key per message, it overtakes Cryptogams at about
  640 bytes.

`experimental/openssl-c906/test-poly1305-dispatch.py --negative-control` (CI)
compiles the patched glue with C models of Cryptogams (h2 left unreduced)
and of the vector function (h[1] left above 2^26). It checks 4000 cases
against the model Poly1305 and requires that no system call is made.
Breaking any of the following fails: the syscall-free path, the threshold, the h2 fold, h2 in the top limb, or the carry before
radix 2^64.

Stand, 2026-11-09:
- `experimental/openssl-c906/poly1305-check.c tags` produces the same 6000
  tags (lengths to 65 KB, offsets 0-15, update splits, all-ones keys and
  messages) as Cryptogams (measured with the opt-in variable of an earlier
  revision; the code path is the same).
- `evp-bench 0` passes.

MB/s, Cryptogams -> vector; `EVP_MAC` is three alternating rounds:

| | 1024 B | 1400 B | 4096 B | 16 KiB |
| --- | ---: | ---: | ---: | ---: |
| `EVP_MAC` Poly1305, fresh key | 122-131 -> 126-149 | 126-143 -> 138-168 | 179-180 -> 240-256 | 194-200 -> 294-310 |
| `openssl speed -evp chacha20-poly1305` (vector ChaCha20 in both) | | 62.9-63.3 -> 65.9-66.3 | | 74.0-74.2 -> 85.1-85.7 |

Below 640 bytes the vector function is not called; there the rounds differ
from each other by up to 15%, more than between the two settings.
