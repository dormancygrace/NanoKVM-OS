# OpenSSL SG2002 Triple DES qualification

`0007-tdes-dma.patch` enables two-key and three-key Triple DES ECB/CBC through
SG2002. A successful whole-block prefix advances the IV before OpenSSL resumes
software over any remaining input. Padding, partial updates and other modes
remain upstream. The 24-byte private raw-key storage participates in existing
context duplication and clear-free. FIPS code is unchanged.

The common block helper passes 5640 ASan/UBSan continuation cases. Native EVP
verification covers 1944 cases across 18 modes, including both TDES key sizes,
with identical baseline and candidate fingerprints. DMA completion counters
confirm that the hardware path ran without poisoning the device.

Three rotated trials on the physical C906 measured EDE3-CBC at 1024 bytes:
3.249 MiB/CPU-s in software versus 7.578 with DMA. At 16 KiB the result was
3.484 versus 32.17 MiB/CPU-s. The minimum remains 1024 bytes. RSS ranges overlap
across these short runs and do not establish a precise incremental RAM cost.
The upstream `-O3` DES software already uses C906 indexed loads, bit extraction,
rotates and unrolled rounds; no alternative software core has been selected.
