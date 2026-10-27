# OpenSSL SG2002 Triple DES qualification

`0007-tdes-dma.patch` uses direct SG2002 DMA for complete ECB/CBC prefixes starting
at 1024 bytes. OpenSSL retains buffering, padding, IV continuation and the
original C906/O3 software below the minimum or after hardware absence/failure.
No ENGINE, cryptodev or new software assembly implementation is introduced.
The context grows from 584 to 608 bytes for the hardware key; output scratch is
operation-local, not retained in each EVP context.

Three rotated native threshold-selection trials and their original binary
identities are retained in this directory. Those timings predate the later
restricted-ISA/sandbox-policy changes and are not labelled as measurements of
later builds. Upstream O3 software is retained for small inputs.

Shared host ASan/UBSan tests cover continuation and 1944 independent EVP cases
across 18 AES/DES/TDES block modes. Prior physical runs passed native legacy
provider loading, all block cases, upstream DES vectors and hardware-counter
checks. The final restricted-ISA stack also passes the full native block, digest,
CTR/GCM carry, AEAD and upstream vector suites with DMA enabled and disabled.
`native-final-validation.json` pins both libcrypto and the legacy provider,
including the selected AES lookup/tail changes and 20,736 independent AES
tail/IV checks. TDES retains a 1024-byte continuation minimum as well as its
initial minimum. These legacy algorithms are not selected for modern TLS.

Historical EDE3-CBC encryption: 1 KiB 3.249→7.578 MiB/CPU-s,
16 KiB 3.484→32.17. The selected minimum is 1 KiB.
