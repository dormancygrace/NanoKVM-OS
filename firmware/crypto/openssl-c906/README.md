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

Selection uses Linux `riscv_hwprobe` vendor key 11 (T-Head), bit 0
(XTheadVector), through OpenSSL's existing startup probe. The query covers
all online CPUs and uses their common capabilities. No `/proc/cpuinfo` access
occurs on the cipher path. Missing headers, an unsupported syscall/key or
an `OPENSSL_riscvcap` override that skips automatic probing leaves this path
disabled. Standard RVV 1.0 alone does not enable draft XTheadVector code.

Inputs below the single `OSSL_CHACHA_C906_MIN_BYTES` threshold (320 bytes, one
five-block group; the C glue only hands whole groups to the vector code) use
upstream code.

The same startup probe, in libcrypto's constructor, also asks
`PR_RISCV_V_GET_CONTROL` once, and the C906 paths (ChaCha20 and GHASH) are
used only if vector state is enabled for the thread that loaded libcrypto.
The cipher path itself makes no system call. Earlier versions asked on every
call, which a seccomp filter installed later can kill: OpenSSH's
pre-authentication filter does. So they needed an explicit opt-in that only
the image downloader's curl set. Threads inherit their creator's vector
control, so the load-time answer holds for the process. A thread that turns
its own vector state off after libcrypto is loaded must not reach these paths.
`NANOKVM_OPENSSL_CHACHA20=generic` (and `NANOKVM_OPENSSL_GHASH=generic`) keep
upstream code; other values, including the former opt-in `auto`, are the
default. The variables are read with `secure_getenv`, so a setuid program
ignores them and takes the default; it no longer falls back to upstream code
as it did under the opt-in. A process started with vector state off uses
upstream code in every thread.

On the stand, `openssl speed -evp chacha20-poly1305`, three alternating
rounds, 1400 B records: 60.2-61.7 MB/s with the per-call query (`auto`),
62.7-63.5 MB/s without it; 16 KiB unchanged (73.4-74.6 MB/s).

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
no EVP result. It is compiled only for RISC-V64, outside the FIPS module. See
`firmware/crypto/experimental/openssl-hwcrypto/` for fault tests and lifetime rules.

## SHA-224/256

`0003-sha256-dma.patch` uses the shared direct transport for full compression
chunks. OpenSSL owns streaming, padding, digest lengths and software fallback.
The FIPS module is excluded. See `experimental/openssl-sha256` for qualification;
the selected 4 KiB threshold and scalar adapter have native qualification.

## AES

`0005-aes-c906.patch` separately optimizes the existing scalar AES table lookups
with XTheadBb byte extraction and scaled XTheadMemIdx loads. The original
RISC-V/FIPS assembly remains the fallback. It changes neither tables nor key
schedules.

`0005-aes-dma.patch` adds ECB/CBC/CTR and the AES-CTR primitive used by GCM.
OpenSSL retains streaming, padding, GHASH, authentication and TLS handling.
The shared block helper commits only completed chunks and supports separate
full-width and GCM counters. See `experimental/openssl-aes` for qualification.

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
definitions).

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
