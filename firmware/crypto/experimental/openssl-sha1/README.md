# OpenSSL SG2002 SHA-1 qualification

`0004-sha1-dma.patch` is the separate SHA-1 integration. It offloads full raw
compression chunks through the direct SG2002 ioctl transport; OpenSSL retains
its SHA-1 initialization, buffering, message length, padding and final digest.
Short input and hardware absence/failure use the original scalar code. Only
successful chunks update the five chaining words; the remainder continues in
software from that state. Both normal and small-footprint implementations are
wrapped, and the FIPS module is excluded. No digest context growth is required.

The initial 4 KiB minimum is a candidate pending native crossover measurements,
including open/capability overhead. Our separate Go scalar SHA-1 implementation
must also be measured against the C906/O3 OpenSSL baseline before replacement.

Validation completed: a full OpenSSL 3.5.9 RISC-V cross-build and
`python3 firmware/crypto/experimental/openssl-sha1/test-fallback.py` with
960 ASan/UBSan cases covering unaligned input, absent hardware, first/later
failures, exact software continuation and preserved OpenSSL buffering fields.
The native digest helpers in `../openssl-sha256/` include SHA-1, context copies,
streaming boundaries and expected digest fingerprint `768fd5312ca481be` from
864 SHA-1/SHA-224/SHA-256 cases.

Native correctness/counters, software candidate comparison, size threshold,
RAM and concurrent-video measurements remain pending.

Native C906: 864 digest EVP cases passed; hardware counters confirmed SHA-1 submissions. Three rotated trials found a 4 KiB call slower with DMA (42.54 versus 54.01 MiB/CPU-s), while 8 KiB reached 68.14 versus 55.44 and 16 KiB 92.55 versus 56.14. The minimum is therefore 8 KiB; each submitted request remains 4 KiB. The separate GNU adapter of the Go scalar core passed correctness but achieved only 41.78 MiB/CPU-s at 16 KiB, so upstream OpenSSL software is retained. GCC already emits C906 rotates and byte swaps for it.
