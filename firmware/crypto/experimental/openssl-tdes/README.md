# OpenSSL SG2002 Triple DES qualification

`0007-tdes-dma.patch` enables two-key and three-key Triple DES ECB/CBC through
SG2002. A successful whole-block prefix advances the IV before OpenSSL resumes
software over any remaining input. Padding, partial updates and other modes
remain upstream. The 24-byte private raw-key storage participates in existing
context duplication and clear-free. FIPS code is unchanged.

The RISC-V OpenSSL 3.5.9 build passes without warnings. The common block helper
passes 5640 ASan/UBSan continuation cases; the independent EVP reference covers
1944 cases across 18 modes including both TDES key sizes. The initial 1024-byte
threshold, native correctness, driver counter evidence, throughput and RAM
remain unqualified. Run `../openssl-des/verify.c` with the baseline and candidate
libraries on the physical C906 before selecting a threshold.
