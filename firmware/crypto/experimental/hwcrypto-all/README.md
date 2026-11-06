# SG2002 CryptoDMA diagnostic tools and experiments

These are opt-in developer diagnostics for the current driver in `platform/modules/hwcrypto`, plus historical measurements. Restoring this directory does not install the tools, enable algorithms in applications, or change firmware outputs.

This extends the existing serialized `nanokvm_hwcrypto` owner with AES-128/192/256, DES, two-key and three-key Triple DES, SM4 (ECB/CBC/CTR), SHA-1, SHA-256 and Base64 encode/decode. All 24 algorithm/mode variants passed short physical-device differential checks. AES-GCM is demonstrated by a userspace hybrid: hardware AES + software GHASH.

The existing AES-128-CTR ioctl remains unchanged and has a compatibility check. The driver also registers Linux Crypto API algorithms for kernel users. The private diagnostic ioctl remains root-only. Build the current driver through `platform/build.sh`; these diagnostics are built separately.

## ABI and ownership

`hwcrypto.h` defines ABI version 1. `HWCRYPTO_INFO` reports capabilities, poison status and per-algorithm submission/completion counters. `HWCRYPTO_RUN` accepts at most 4096 input bytes. Input/output fields are userspace virtual pointers copied by the driver, never caller-provided DMA addresses. A separate 8192-byte output allocation accommodates Base64 expansion. The same mutex, controller and descriptor serialize the legacy and new operations.

AES/DES/TDES/SM4 ECB and CBC require whole blocks; CTR accepts a partial last block, and the caller must continue its counter correctly across requests. No padding is added for ECB/CBC. SHA requests are raw compression transforms over multiples of 64 bytes; userspace initializes the state and supplies final SHA padding. The benchmark provides working streaming examples. Base64 decode requires contiguous padded input with no whitespace, and output capacity for `length / 4 * 3` bytes; actual output length excludes final padding. This is not a permissive MIME Base64 decoder.

Descriptor and payload mappings use explicit CPU/device ownership synchronization. A 20 ms bounded completion wait is retained. SG2002 TRM Table 22.3 defines status bit 0 for encryption completion and bit 1 for hashing: expected values are 1 for ciphers/Base64 and 2 for SHA. Unexpected completion/timeout poisons and pins the module; memory mappings are retained when DMA ownership is uncertain. Touched payload bytes and the descriptor are cleared after a successful operation. The extension does not access protected SEC_SYS registers or change clocks/OpenSBI.

## Build and reproduce

The driver source, including `hwcrypto.h`, is `platform/modules/hwcrypto`; `platform/build.sh modules` builds `nanokvm_hwcrypto.ko` with the released kernel. Build the userspace tools with the firmware cross-toolchain against its headers:

```sh
H=-Iplatform/modules/hwcrypto
"${CROSS_COMPILE}gcc" -O2 -Wall -Wextra -Werror $H -o crypto-bench firmware/crypto/experimental/hwcrypto-all/crypto-bench.c -lcrypto
"${CROSS_COMPILE}gcc" -O2 -Wall -Wextra -Werror $H -o gcm-bench firmware/crypto/experimental/hwcrypto-all/gcm-bench.c -lcrypto
"${CROSS_COMPILE}gcc" -O2 -Wall -Wextra -Werror $H -o hwcrypto-info firmware/crypto/experimental/hwcrypto-all/crypto-info.c
```

After installing on the matching device, `crypto-bench --kat` checks boundaries, encryption/decryption, SHA padding/chaining, Base64 and invalid requests. `crypto-bench --bench 32` also measures 64/256/1200/4096/65536-byte inputs. `gcm-bench --kat` checks the AES-GCM hybrid; without arguments it measures hybrid versus software AES-GCM and software ChaCha20-Poly1305. These programs use synthetic keys/nonces and are measurement tools, not application encryption APIs. DES/TDES and SHA-1 are included to test the hardware ABI against a software reference; their presence is not a recommendation to use them for application security. Invalid GCM tags must be rejected; decrypted output must not be consumed before authentication succeeds.

`python3 summarize.py RESULT_DIRECTORY` produces medians and tables from the saved CSV outputs.

## Measurements and limits

The results below describe the September 2026 experiments and their original software environment, including OpenSSL 3.6.4. They are retained for reproducibility, not presented as measurements of the current OpenSSL 4 firmware. `summarize.py` expects the original named CSV inputs; it is not a general-purpose report generator.

For 16 KiB AES-256-GCM records, OpenSSL software measured 5.90 MiB/s and 2.604 ms CPU; hardware AES + software GHASH measured 12.03 MiB/s and 1.196 ms CPU (54% less CPU work). Base64 decoding is an exception to hardware benefits: at 87,384 input bytes it used 23.22 ms CPU/MiB versus 15.87 in software. Small operations can also lose to ioctl/copy overhead; hardware selection must be based on workload size.

These are functional checks and bounded microbenchmarks with capture disabled, not long concurrent video/TLS loads. GHASH/HMAC/GCM are not separate native SG2002 CryptoDMA engines. TRNG is a separate block and was not included in these deterministic-algorithm measurements. SM3 appears in a newer generic vendor driver, but SG2002 TRM marks its descriptor bit reserved; it is not advertised here.

## Primary sources

- [SG2002 TRM v1.02, chapter 22](https://github.com/sophgo/sophgo-doc/releases/download/sg2002-trm-v1.02/sg2002_trm_en_v1.02.pdf), especially overview and completion bits, printed pages 789 and 791.
- [Pinned SOPHGO driver](https://github.com/sophgo/linux_5.10/blob/382af279605297cbe3d0181a3e46dc5d004cee3e/drivers/crypto/cvitek-spacc.c) for descriptor field conventions. Its unbounded/global completion handling is not copied.
- [OpenSSL 3.6.4 GCM core](https://github.com/openssl/openssl/blob/openssl-3.6.4/crypto/modes/gcm128.c).

## Linux Crypto API

`crypto_linux_api.inc` supplies 12 skciphers and two asynchronous hashes. Sleepable callers execute inline; atomic callers use an ordered workqueue. Each operation releases shared DMA ownership between 4 KiB chunks. Hash requests own their streaming state, final padding and export/import data. No caller can supply a DMA address.

`hwcrypto-info` reads native completion/poison counters without submitting DMA. Build the utility separately with the firmware cross compiler from `crypto-info.c`; its presence on a device depends on explicit installation or image staging.

The `/dev/crypto` (cryptodev) benchmarks were removed with the cryptodev module in 4.0.0.
