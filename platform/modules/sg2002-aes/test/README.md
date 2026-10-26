# SG2002 CMAC and AEAD qualification

Build the driver and these test modules against the device's prepared kernel
and matching configuration, with the repository's kernel CPU profile. Keep
build output outside the source checkout. Load test modules from RAM on an
exclusively owned stand, with no active users of the crypto module when replacing
it. Restore the installed driver afterwards; no installed-file backups are needed.

`sg2002_cmac_test.ko` checks `sg2002-cmac-aes` against the independent AES-CMAC
library. For example, `insmod sg2002_cmac_test.ko cases=2000 bench_ms=700`
checks RFC 4493's four vectors in 32 request modes, AES-128/192/256, empty and
partial blocks, chained scatterlists and virtual input, synchronous and queued
requests, streaming update/final/finup, export/import into another request,
invalid keys/state, and four concurrent streams with different keys. Sizes
cover 4 KiB DMA boundaries through 64 KiB. Successful insertion means all
checks passed; inspect `cmac_test:` messages and unload afterwards. Setting
`bench_ms=0` skips the comparison with the software `cmac-aes-lib` provider.

`sg2002_ccm_test.ko` compares the explicitly selected `sg2002-ccm-aes` driver with
the synchronous software `ccm(aes)` implementation. Its default 3000 cases cover:

- RFC 3610 packet vector 1, AES-128/192/256, every CCM tag and nonce length.
- Empty data, partial blocks, 4 KiB chunk boundaries, and the `0xff00` AAD length
  encoding transition, with up to 64 KiB each of AAD and payload.
- Chained scatterlists, in-place and separate output, synchronous and queued
  requests, and independent corruption of AAD, ciphertext, tag and nonce.
- Invalid keys, tag sizes, IV parameters, too-short ciphertext and length overflow.
- Four concurrent CCM/GCM streams with distinct keys sharing the engine.

For example, `insmod sg2002_ccm_test.ko cases=3000 bench_ms=700` runs correctness
checks and then timings; successful insertion means all checks passed. Inspect
the `ccm_test:` kernel messages and unload the test module afterwards. Setting
`bench_ms=0` skips timing. The test performs no persistent writes. Timing reuses
synthetic keys/nonces and must never be adapted to encrypt user data that way.

`sg2002_gcm_test.ko` remains the existing GCM regression test. It intentionally
returns `-EAGAIN` after a successful run; require its reported zero failures,
not merely a failed insertion.

## Measured on SG2002, Linux 7.3, 2026-10-26

AES-128, 16-byte tags; the algorithm timing has 32 bytes of AAD and a 64 KiB
payload, using the same shared engine and production GCM implementation:

| Path | CCM | GCM |
| --- | ---: | ---: |
| AEAD encryption | 62.8 MiB/s | 26.6 MiB/s |
| AEAD decryption | 61.8 MiB/s | 28.9 MiB/s |
| Direct CIFS sequential source | 8.58–8.67 MiB/s | 7.43–7.57 MiB/s |
| Complete server source, relay + IPC | 7.97–8.03 MiB/s | 6.81–7.04 MiB/s |

The network fixture used Samba 4.23.8, SMB3.1.1, a read-only share requiring
encryption and a deterministic 64 MiB image over 100 Mbit/s Ethernet. Force one
cipher at a time with Samba's `server smb3 encryption algorithms`, reload the
fixture configuration and create a fresh session. Check `smbstatus -j` for the
negotiated cipher and full encryption on the share, and `/proc/crypto` for active
references to the expected hardware driver. Do not silently benchmark a different
cipher. The client used `cache=none,actimeo=0,rsize=1048576`, 128 KiB reads and
the source's adaptive 1 MiB window. Each path/cipher has two sessions with two
sequential rounds; the order alternated CCM/GCM. The fixture was reached through
the same Windows TCP proxy and WSL relay in both modes.

Full-image SHA-256 matched in all four server-source sessions. The whole-device
CPU was busy about 96–97% with CCM and 98–99% with GCM. Small random reads were
variable (one CCM server-source pass averaged 16 ms against 4.9 ms in another),
so these runs do not establish a random-read latency improvement. These are
source and AEAD timings, not USB throughput measurements.

The hardware run passed 3000 differential/tamper cases; the existing GCM test
passed 1000. A separate test-only build set the failure latch immediately after
initialization, without submitting DMA or inducing a hardware timeout: software
fallback was checked with the same vectors and concurrent streams. Production
contains no fault-injection switch. A standalone RFC vector increased the AES
DMA submitted/completed counters by exactly three (two CBC operations and one
CTR operation); the engine remained unpoisoned during hardware qualification.

## AES-CMAC signing, same stand and fixture

Use the signed, unencrypted `images` share and restrict the Samba fixture to
`server smb3 signing algorithms = AES-128-CMAC`. Verify full CMAC signing and
no encryption with `smbstatus -j`. The order was original software, hardware,
original software, Crypto API software, hardware; each session ran two full
sequential rounds. The complete reader additionally checked the full 64 MiB
SHA-256. All five hashes matched. Read sizes, caching and network relays were
the same as above.

| Signing implementation | Direct CIFS | Complete server source |
| --- | ---: | ---: |
| Original CIFS AES library | 6.15–6.20 MiB/s | 5.33–5.80 MiB/s |
| Crypto API, software CMAC | 5.98–6.18 MiB/s | 5.81 MiB/s |
| Crypto API, SG2002 CMAC | 9.27–9.60 MiB/s | 8.65–8.72 MiB/s |

The normal server path improves about 50% over software signing. Hardware
sessions increased both AES DMA counters by 34,594 (direct) and 51,374
(complete reader); software sessions left them unchanged. The hardware latch
remained clear. Whole-device busy time was about 90–93% directly and 96%
through the reader, against 95–96% and 98% with the original software path.
Random-read latency varied, so no improvement is claimed for it.

Raw 64 KiB CMAC measured 131.4 MiB/s in hardware versus 15.0 MiB/s in software.
DMA setup dominates small messages: at 64 bytes hardware measured 4.3 MiB/s
versus 9.4 MiB/s in software. The network results include that overhead for
SMB requests and metadata; the bulk-read gain must not be generalized to all
message sizes.

### DMA crossover

`insmod sg2002_cmac_test.ko cases=0 crossover=1 bench_ms=400` measures every
16-byte size from 16 through 384 bytes, then 512, 768 and 1024 bytes. Run three
times, unloading between runs. Each size compares both providers with a reused
key and with a newly allocated transform/key per message, including teardown.
Both modes allocate the request per message. The latter matches CIFS's current
cryptographic lifecycle; it excludes filesystem/network processing and SG-list
construction. Compare paired runs rather than extrapolating from bulk throughput.

Three runs on the same SG2002 found the last consistently slower and first
consistently faster tested sizes:

| Key/context lifetime | Last DMA loss | First DMA win | Suggested cutoff |
| --- | ---: | ---: | ---: |
| Reused key/transform | 160 bytes | 176 bytes | 192 bytes |
| New transform/key per MAC (CIFS) | 288 bytes | 304 bytes | 320 bytes |

Median full-lifecycle time per signature, in microseconds (lower is better):

| Message bytes | Software | Hardware | Hardware speedup |
| ---: | ---: | ---: | ---: |
| 64 | 10.96 | 23.40 | 0.47× |
| 128 | 14.95 | 23.61 | 0.63× |
| 256 | 22.64 | 24.28 | 0.93× |
| 288 | 24.58 | 24.89 | 0.99× |
| 304 | 25.64 | 24.86 | 1.03× |
| 320 | 26.36 | 24.93 | 1.06× |
| 384 | 30.66 | 25.59 | 1.20× |
| 512 | 38.31 | 26.82 | 1.43× |
| 1024 | 69.15 | 29.47 | 2.35× |

The 304-byte gain was only 2–5% across individual runs; at 320 bytes it was
5–8%, and at 384 bytes 19–30%. A 320-byte full-message cutoff is therefore
reasonable for this CIFS implementation, or 384 bytes for a larger margin.
This is a CMAC-message crossover, not a universal threshold for individual
CBC DMA chunks or other crypto algorithms. Key reuse changes the crossover,
so retest if CIFS starts caching signing transforms. These measurements describe
the current always-hardware CMAC provider; they do not add automatic size-based
provider selection.

The hardware driver passed 2000 differential cases and all vector/concurrency
checks. A separate fault-injection build, used only in RAM, set the poison
latch and returned `-EIO` after its second CBC operation had completed and
returned DMA ownership. `expect_failure=1 cases=300 bench_ms=0` checks that
the failed request cannot emit a tag or export state, an already-started
unfailed request finishes correctly from its saved CBC chain, and new requests
use software fallback. The complete fallback run, including vectors and
concurrency, submitted exactly three AES operations (one subkey ECB and two
CBC); none occurred after the latch. `expect_failure` belongs only to the test
module and requires this separately instrumented driver. Production has no
fault-injection switch. After restoring the production driver, 200 CMAC,
300 CCM and 1000 GCM regression cases passed with an unpoisoned engine.

On NanoKVM's patched kernel, set `NANOKVM_SMB_TEST_STRICT_SIGNATURES=1` and
`NANOKVM_SMB_TEST_ADDR` when running `TestSMBRejectModifiedSignature` in the
`remotemedia` test binary. Its TCP proxy modifies only signature bytes of READ
and QUERY_INFO responses from the signed fixture. Both must fail, including
required metadata revalidation after optional share probes. The original CIFS
module accepted the modified metadata in the negative control; the patch
preserves signature errors through compound-response handling. Distribution
CI kernels skip this device-specific check. The real signed/encrypted Samba
suite also covers deadlines, cancellation, mount restrictions and cleanup.
