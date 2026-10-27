# OpenSSL SG2002 DES qualification

`0006-des-dma.patch` uses direct SG2002 DMA for complete ECB/CBC prefixes starting
at 1536 bytes. OpenSSL retains buffering, padding, IV continuation and the
original C906/O3 software below the minimum or after hardware absence/failure.
No ENGINE, cryptodev or new software assembly implementation is introduced.
The context grows from 328 to 336 bytes for the hardware key; output scratch is
operation-local, not retained in each EVP context.

Three rotated native threshold-selection trials and their original binary
identities are retained in this directory. Those timings predate the later
restricted-ISA/sandbox-policy changes and are not labelled as measurements of
later builds. Upstream O3 software is retained for small inputs.

Shared host ASan/UBSan tests cover continuation and 1944 independent EVP cases
across 18 AES/DES/TDES block modes. Prior physical runs passed native legacy
provider loading, all block cases, upstream DES vectors and hardware-counter
checks. A final restricted-ISA full-stack native check is recorded separately
when complete. These legacy algorithms are not selected for modern TLS.
