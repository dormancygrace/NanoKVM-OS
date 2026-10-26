# OpenSSL SG2002 SM3 qualification

`0009-sm3-dma.patch` adds SM3 compression through the direct SG2002 ioctl,
keeping OpenSSL buffering, length tracking and padding. A failed request
resumes software from the last confirmed 4 KiB prefix. Small messages and old
modules without the capability bit use upstream SM3. There is no ENGINE or
cryptodev dependency; the FIPS build is unchanged.

The kernel extension follows Sophgo's SM3 implementation added in commit
[6ef521238996](https://github.com/sophgo/linux_5.10/commit/6ef521238996df76fca91baf2d8621791aaa3864)
on 2025-06-23, present in the `sg200x-dev` branch. Descriptor bit 14 and result
registers at 0x1e0 supersede the older SG2002 TRM's reserved-bit description.
The vendor initializes SM3 descriptor words in native order, unlike SHA.
The candidate converts canonical userspace chaining state at the driver edge,
uses a separate bounded destination mapping and retains the existing timeout,
serialization and poison policy. Completion bit 2 and result-word order are
pending physical-device confirmation; passing host tests cannot establish them.

The original RUN request stays 144 bytes and INFO stays 160 bytes with unchanged
ioctl numbers and counter offsets. Algorithm 8 is SM3. A separate 176-byte INFO2
adds its counters; existing applications and libraries retain the old ABI.
The OpenSSL transport checks the capability before attempting the new operation.

Completed checks:

- OpenSSL 3.5.9 RISC-V build and module build for Linux 7.3.0-nanokvm-os-r3.
- ASan/UBSan: 960 partial-completion/failure cases against an independent SM3
  compression implementation validated by the published `abc` vector.
- Extracted-driver checks: descriptor/state conversion, separate DMA destination,
  strict completion/poison behavior, 4098 length cases and old ioctl ABI.
- All eight transport algorithms and the late-output-fault negative control.
- Host EVP reference: 288 cases, fingerprint `1aa2e10b5fb3fe70`, independently
  generated with Python hashlib.

Native checks remain pending. Run `dma-verify.c` first with the candidate module;
it compares complete padded messages and 64-byte versus 4 KiB chaining against
software OpenSSL and verifies SM3 submission/completion counters. `verify.c`
then checks the integrated EVP path, including streaming and context copies.
Use `../openssl-sha256/bench.c SM3` for CPU/wall/RAM measurements. The 4 KiB
threshold remains a candidate, and no hardware correctness or speedup is claimed.
