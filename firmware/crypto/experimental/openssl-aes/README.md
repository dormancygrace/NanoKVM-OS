# OpenSSL SG2002 AES qualification

`0005-aes-dma.patch` adds AES-128/192/256 ECB, CBC and CTR offload to the
default provider, plus the AES-CTR bulk primitive inside AES-GCM. OpenSSL
retains padding, streaming buffers, GHASH, tag verification and TLS processing.
OFB/CFB and the FIPS module retain their original implementations. Private raw
key storage is limited to the AES platform unions and is covered by existing
context copy/clear-free operations.

The shared block helper returns a committed whole-block prefix. CBC decryption
saves the original final ciphertext before in-place output; only successful
requests advance the IV. CTR preserves partial-block handling and distinguishes
full-width counters from GCM's 32-bit counter, splitting requests at its wrap.
Hardware absence or a failure resumes the original software path. Requests are
at most 4 KiB; the initial 1024-byte minimum is an **unqualified candidate**.

Validation completed: full RISC-V OpenSSL 3.5.9 build without warnings and
`python3 firmware/crypto/experimental/openssl-aes/test-blocks.py` under
ASan/UBSan. It checks 5640 in-place/unaligned/guarded CBC/CTR/ECB continuation
cases over 18 AES/DES/TDES/SM4 modes using a software-backed fake accelerator,
and 25 independent GCM CTR32 rollover/failure cases. Testing the shared helper
for DES/TDES/SM4 does not enable those algorithms in this AES patch.

Native EVP known-answer and invalid-tag tests, TLS 1.2/1.3, DMA counters,
threshold/CPU/RAM/concurrent-video measurements remain pending. Use the EVP
and direct-hardware tools in `../openssl-c906` and `../sg2002-crypto-all` for
controls. Compare hardware enabled/disabled against the PR #504 baseline.
