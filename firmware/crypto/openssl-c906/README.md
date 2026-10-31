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

Inputs below the single `OSSL_CHACHA_C906_MIN_BYTES` threshold (640 bytes) use
upstream code. Every eligible call checks the calling thread's vector permission
with `PR_RISCV_V_GET_CONTROL`; the result is never cached across threads.
A failed query returning errno uses software. Fatal seccomp rules cannot be
handled by fallback: acceleration therefore requires the explicit secure
environment opt-in `NANOKVM_OPENSSL_CHACHA20=auto`. Missing, unknown or `generic`
values retain upstream code without issuing the per-call query. Privileged
execution ignores the opt-in. The image downloader enables it only for its
qualified curl child and preserves explicit operator overrides. Other clients
must qualify their syscall policy before opting in. The opt-in cannot force
unsupported instructions or bypass the calling thread's vector permission.

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
