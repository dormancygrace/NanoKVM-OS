# SG2002 direct transport qualification

This layer uses `/dev/sg2002-aes-probe` and the private version-1 ioctl ABI from
`platform/modules/sg2002-aes/sg2002_crypto.h`. It does not use `/dev/crypto`,
cryptodev, ENGINE or a new kernel module. The transport supports all seven
advertised algorithms; each algorithm's OpenSSL integration and measured size
threshold belongs in its own patch/PR.

Every operation owns a close-on-exec descriptor and checks capabilities. No
userspace lock, shared request/key/output, or descriptor cache crosses fork,
provider unload or threads. Requests remain bounded to the driver's 4 KiB
maximum. Callers split large operations and retain the original software
state until success. Cipher/Base64 output is committed from private scratch;
hashes do not allocate that scratch. An interrupted or failed final copy
cannot corrupt in-place input before software fallback. Controller failures
are latched for the process, while interrupted/resource failures allow later
operations. `NANOKVM_OPENSSL_DMA=off` is a diagnostic software-only control.

Run `python3 firmware/crypto/experimental/openssl-sg2002/test-transport.py`.
The host ASan/UBSan test injects 56 submissions across all seven algorithms:
success, EINTR, EIO, a late EFAULT after writes to private scratch, invalid
completion status and oversized output. It checks unchanged caller state and
in-place data on failure, retry/disable policy and descriptor lifecycle.
It also covers permission failure, non-device paths, incompatible ABI and
poisoned-controller capability reports. These are transport fault tests;
physical hardware correctness and profitability require native measurements.
