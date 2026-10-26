# OpenSSL C906 AES qualification

`0005-aes-dma.patch` adds C906 indexed table loads to OpenSSL's scalar AES
assembly and SG2002 offload for AES-128/192/256 ECB, CBC and CTR, including
the CTR primitive inside AES-GCM. Padding, streaming, GHASH, authentication,
TLS and the FIPS module retain their existing implementations. The indexed
loads use the original tables and key schedules; their memory footprint is
unchanged. The ordinary RISC-V assembly remains the fallback build path.

The shared DMA helper commits only successful whole-block prefixes. CBC
in-place decryption preserves the last ciphertext; IVs advance only after
success. CTR retains partial blocks and splits at GCM's 32-bit counter wrap.
Requests are bounded at 4096 bytes. Hardware absence or failure resumes software.

Three rotated native trials compared the same OpenSSL 3.5.9 C906/O3 flags.
With DMA disabled, indexed loads improved AES-128-GCM opening by 16.9% at
64 bytes and 11.5% at 1024 bytes; AES-256-GCM improved by 18.7% and 15.1%.
AES-128 ECB/CBC/CTR at 1024 bytes improved by 18.1%, 20.9% and 17.4%.
These are MiB per process CPU-second, measured through EVP with unaligned input.

DMA minima were selected against this faster software: 2560 bytes for ECB/CBC,
2048 for CTR and GCM. Across both directions and all AES key sizes, the smallest
measured advantage at those minima was 13.2% for ECB, 16.0% for CBC and 26.3%
for CTR; AES-128-GCM improved by at least 10.5% at 2048 bytes. A 1024-byte DMA
minimum regressed several modes. These thresholds deliberately use one minimum
per mode rather than depending on key length or direction.

Native verification passed 1944 block-mode cases, 408 AEAD cases with rejected
invalid tags, and upstream AES vectors (313 tests, one FIPS-only skip). Baseline
and candidate fingerprints match. Driver counters confirm AES use without a
poisoned device. The host continuation suite covers threshold boundaries,
first/later failures, offsets, in-place output and GCM counter rollover under
ASan/UBSan. Final-stack native and HTTPS checks follow integration.
