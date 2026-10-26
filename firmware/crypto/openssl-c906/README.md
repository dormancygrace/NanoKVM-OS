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

Selection requires `xtheadvector` in every CPU ISA line and vector state enabled
for the calling thread through `PR_RISCV_V_GET_CONTROL`. Missing information,
unsupported kernels, disabled threads and inputs shorter than 320 bytes use
upstream code. The current-thread control is checked at each eligible call,
including after a context moves between threads. The environment variable
`NANOKVM_OPENSSL_CHACHA20=generic` disables this path for diagnosis; it cannot
force unsupported instructions and is ignored for privileged execution.

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

`0002-sg2002-transport.patch` adds the shared private ioctl client for subsequent
per-algorithm patches. On its own it selects no hardware algorithm and changes
no EVP result. It is compiled only for RISC-V64, outside the FIPS module. See
`firmware/crypto/experimental/openssl-sg2002/` for fault tests and lifetime rules.

## SHA-224/256

`0003-sha256-dma.patch` uses the shared direct transport for full compression
chunks. OpenSSL owns streaming, padding, digest lengths and software fallback.
The FIPS module is excluded. See `experimental/openssl-sha256` for qualification;
the current 4 KiB threshold still requires physical crossover measurements.

## SHA-1

`0004-sha1-dma.patch` separately wraps SHA-1 compression, preserving upstream
streaming/padding and software continuation. It excludes the FIPS module.
See `experimental/openssl-sha1` for fault tests and pending native qualification.

## AES

`0005-aes-dma.patch` adds ECB/CBC/CTR and the AES-CTR primitive used by GCM.
OpenSSL retains streaming, padding, GHASH, authentication and TLS handling.
The shared block helper commits only completed chunks and supports separate
full-width and GCM counters. See `experimental/openssl-aes` for qualification.

## DES

`0006-des-dma.patch` enables legacy-provider ECB/CBC with software continuation
and private raw-key storage. The initial 1 KiB threshold remains unqualified.
The transport environment check uses a public libc entry point so the separate
legacy module has no dependency on libcrypto's private symbols. See
`../experimental/openssl-des` for the shared block-mode EVP reference and limits.

## Triple DES

`0007-tdes-dma.patch` covers ECB/CBC with two or three keys, preserving upstream
stream state and fallback. Its 1 KiB candidate minimum needs native validation;
see `../experimental/openssl-tdes`.
