# OpenSSL SG2002 DES qualification

`0006-des-dma.patch` enables DES ECB/CBC through the existing root-only SG2002
ioctl, preserving the legacy provider, padding, streaming and software fallback.
An eight-byte private raw-key field follows the existing context copy/clear-free
lifecycle. The initial 1024-byte minimum is pending physical C906 measurement.
Other DES modes retain their original software implementations.

The legacy module uses libc `secure_getenv` for the transport switch rather than
an unexported libcrypto internal symbol. A full RISC-V OpenSSL 3.5.9 build passes
without warnings; the legacy DSO has no unresolved private `ossl_*` symbols.
The transport fault suite also applies subsequent transport changes before
running its ASan/UBSan checks and negative control.

`verify.c` is the shared native EVP verifier for 18 AES/DES/TDES/SM4 modes.
It pins host OpenSSL's independent ciphertext fingerprint
`a37680a0195705fb` and covers 1944 encrypt/decrypt cases with partial updates,
context copies, unaligned/in-place buffers and guards. The host ASan/UBSan run
passes. Native provider loading, EVP vectors, DMA counters, throughput and RAM
measurements are pending; no speedup is claimed.
