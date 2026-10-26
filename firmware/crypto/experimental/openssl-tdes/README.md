# OpenSSL TDES native qualification

`0007-tdes-dma.patch` handles two-key and three-key ECB/CBC with the shared direct DMA transport. Use `../openssl-des/verify.c` for the shared block-mode correctness checks.

Physical C906: the 1944-case shared verifier and upstream vectors pass with DMA counters confirming TDES submissions. In three rotated trials, TDES-EDE3-CBC decryption reached 7.58 versus 3.25 MiB/CPU-s upstream at 1 KiB, and 32.17 versus 3.48 at 16 KiB. The 1 KiB minimum is retained. Small messages keep OpenSSL's already-unrolled DES scalar core, whose O3 code uses C906 bit extraction, rotates and indexed loads.
