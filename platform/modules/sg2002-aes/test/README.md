# SG2002 AEAD qualification

Build the driver and these test modules against the device's prepared kernel
and matching configuration, with the repository's kernel CPU profile. Keep
build output outside the source checkout. Load test modules from RAM on an
exclusively owned stand, with no active users of the crypto module when replacing
it. Restore the installed driver afterwards; no installed-file backups are needed.

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
