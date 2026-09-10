# SG2002 CryptoDMA algorithm extension

This extends the existing serialized `sg2002_aes_probe` owner with AES-128/192/256, DES, two-key and three-key Triple DES, SM4 (ECB/CBC/CTR), SHA-1, SHA-256 and Base64 encode/decode. All 24 algorithm/mode variants passed short physical-device differential checks. AES-GCM is demonstrated by a userspace hybrid: hardware AES + software GHASH.

The existing AES-128-CTR ioctl remains unchanged and has a compatibility check. The new module is installed on the development device and selected by the module build script. The module now also registers Linux Crypto API algorithms, exposed to userspace through standard cryptodev-linux `/dev/crypto`. The private diagnostic ioctl remains root-only. OpenSSL is built with an opt-in `devcrypto` engine; Go/Pion do not automatically use it. The extension is included in the 2026-09-10 beta full image; application-only packages do not replace the kernel modules.

## ABI and ownership

`sg2002_crypto.h` defines ABI version 1. `SG2002_CRYPTO_INFO` reports capabilities, poison status and per-algorithm submission/completion counters. `SG2002_CRYPTO_RUN` accepts at most 4096 input bytes. Input/output fields are userspace virtual pointers copied by the driver, never caller-provided DMA addresses. A separate 8192-byte output allocation accommodates Base64 expansion. The same mutex, controller and descriptor serialize the legacy and new operations.

AES/DES/TDES/SM4 ECB and CBC require whole blocks; CTR accepts a partial last block, and the caller must continue its counter correctly across requests. No padding is added for ECB/CBC. SHA requests are raw compression transforms over multiples of 64 bytes; userspace initializes the state and supplies final SHA padding. The benchmark provides working streaming examples. Base64 decode requires contiguous padded input with no whitespace, and output capacity for `length / 4 * 3` bytes; actual output length excludes final padding. This is not a permissive MIME Base64 decoder.

Descriptor and payload mappings use explicit CPU/device ownership synchronization. A 20 ms bounded completion wait is retained. SG2002 TRM Table 22.3 defines status bit 0 for encryption completion and bit 1 for hashing: expected values are 1 for ciphers/Base64 and 2 for SHA. Unexpected completion/timeout poisons and pins the module; memory mappings are retained when DMA ownership is uncertain. Touched payload bytes and the descriptor are cleared after a successful operation. The extension does not access protected SEC_SYS registers or change clocks/OpenSBI.

## Build and reproduce

Use the matching ordinary kernel source/output and the firmware cross-toolchain. The preparation script requires a fresh output directory and verifies the pinned baseline source through the existing streaming-descriptor preparer.

```sh
python3 firmware/crypto/experimental/sg2002-crypto-all/prepare.py --repo "$PWD" --output "$MODULE_OUT"
make -C "$KERNEL_SOURCE" O="$KERNEL_OUTPUT" ARCH=riscv CROSS_COMPILE="$CROSS_COMPILE" M="$MODULE_OUT" modules
"${CROSS_COMPILE}gcc" -O2 -Wall -Wextra -Werror -o crypto-bench firmware/crypto/experimental/sg2002-crypto-all/crypto-bench.c -lcrypto
"${CROSS_COMPILE}gcc" -O2 -Wall -Wextra -Werror -o gcm-bench firmware/crypto/experimental/sg2002-crypto-all/gcm-bench.c -lcrypto
```

After installing on the matching device, `crypto-bench --kat` checks boundaries, encryption/decryption, SHA padding/chaining, Base64 and invalid requests. `crypto-bench --bench 32` also measures 64/256/1200/4096/65536-byte inputs. `gcm-bench --kat` checks the AES-GCM hybrid; without arguments it measures hybrid versus software AES-GCM and software ChaCha20-Poly1305. These programs use synthetic keys/nonces and are measurement tools, not application encryption APIs. Invalid GCM tags must be rejected; decrypted output must not be consumed before authentication succeeds.

`python3 summarize.py RESULT_DIRECTORY` produces medians and tables from the saved CSV outputs. The Go experiment and a narrowly scoped runtime patch are in `ghash-go/`.

## Measurements and limits

The [public qualification summary](../../../../docs/VALIDATION.md) separates short functional checks, matched kernel batches and endurance limitations. CPU time is distinct from wall throughput.

For 16 KiB AES-256-GCM records, OpenSSL software measured 5.90 MiB/s and 2.604 ms CPU; hardware AES + software GHASH measured 12.03 MiB/s and 1.196 ms CPU (54% less CPU work). Base64 decoding is an exception to hardware benefits: at 87,384 input bytes it used 23.22 ms CPU/MiB versus 15.87 in software. Small operations can also lose to ioctl/copy overhead; hardware selection must be based on workload size.

These are physical-device functional checks and bounded microbenchmarks with capture disabled. They do not qualify long concurrent video/TLS loads or explain prior H.265/AES SoC hangs. GHASH/HMAC/GCM are not separate native SG2002 CryptoDMA engines. TRNG is a separate block and was not included in these deterministic-algorithm measurements. SM3 appears in a newer generic vendor driver, but SG2002 TRM marks its descriptor bit reserved; it is not advertised here.

## Primary sources

- [SG2002 TRM v1.02, chapter 22](https://github.com/sophgo/sophgo-doc/releases/download/sg2002-trm-v1.02/sg2002_trm_en_v1.02.pdf), especially overview and completion bits, printed pages 789 and 791.
- [Pinned SOPHGO driver](https://github.com/sophgo/linux_5.10/blob/382af279605297cbe3d0181a3e46dc5d004cee3e/drivers/crypto/cvitek-spacc.c) for descriptor field conventions. Its unbounded/global completion handling is not copied.
- [OpenSSL 3.6.4 GCM core](https://github.com/openssl/openssl/blob/openssl-3.6.4/crypto/modes/gcm128.c).

## Standard interface and OpenSSL

`crypto_linux_api.inc` supplies 12 skciphers and two asynchronous hashes. Sleepable callers execute inline; atomic callers use an ordered workqueue. Each operation releases shared DMA ownership between 4 KiB chunks. Hash requests own their streaming state, final padding and export/import data. No caller can supply a DMA address.

`/dev/crypto` uses unmodified public cryptodev IDs and has the upstream access mode 0666. Sessions copy keys and hold algorithm references. `CIOCGSESSINFO` reports the selected driver. `sg2002-crypto-info` reads native completion/poison counters without submitting DMA. The utility is installed on the development device. Build it separately with the firmware cross compiler from `crypto-info.c`; diagnostic utility availability depends on image staging.

```sh
openssl engine -t -c devcrypto
openssl speed -elapsed -seconds 3 -bytes 16384 -engine devcrypto -evp aes-256-ctr
openssl dgst -engine devcrypto -engine_impl -sha256 example.bin
```

The OpenSSL engine advertises AES-128/192/256 ECB/CBC/CTR, DES/3DES CBC, SHA1/SHA256. It does not implement AES-GCM. Standard `CIOCAUTHCRYPT` **does** provide AES-GCM using Linux `gcm(aes)` with hardware AES-CTR and software GHASH. Use `cryptodev-gcm-bench.c` to reproduce that path; use `cryptodev-bench.c` for the other standard algorithms. Compile either with the cross compiler, `-O2 -Wall -Wextra -Werror -Ifirmware/crypto/cryptodev-linux -lcrypto`. Full measurements, the tag-length session limitation, and installation/release boundaries are in [standard API evidence](../../../../docs/VALIDATION.md).

### Longer matched standard-API batches

`cryptodev-bench --bench` still defaults to 32 operations and all three block
sizes. For a longer comparison without running every size, use:

```sh
SG2_BENCH_COUNT=1024 SG2_BENCH_BYTES=16384 ./cryptodev-bench --bench
```

Count accepts 1–4096; bytes accepts 0 (all), 1200, 16384 or 65536. Malformed
settings exit with status 2 before opening either crypto device. Every invocation
retains all 14 standard-API correctness cases and checks hardware/software DMA
completion counters around the timed batches. Stop video services in each
comparison leg to exclude unrelated SRTP work. `BENCH_CONFIG` records the chosen
controls; `RESULT` records the actual operation count. Do not compare short
32-operation medians directly with long-batch results as though workload and
background services were identical.
