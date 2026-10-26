# OpenSSL SG2002 SM4 qualification

`0008-sm4-dma.patch` enables SM4 ECB/CBC/CTR in the default provider. OpenSSL
retains key schedules, buffered updates, padding, context copies and software
continuation. CTR first consumes an outstanding partial keystream block before
submitting whole blocks; CBC decryption preserves the original ciphertext IV.
The private 16-byte raw-key field follows existing copy/clear-free handling.
OFB/CFB, authenticated modes, newer RISC-V crypto dispatch and FIPS stay upstream.

The complete RISC-V OpenSSL 3.5.9 build passes without warnings. Shared helper
ASan/UBSan tests cover SM4 partial failure and the independent EVP reference
covers ECB/CBC/CTR streaming, duplication and guards. Physical C906 correctness,
DMA counters, CPU/RAM and the initial 1024-byte threshold remain unqualified.
