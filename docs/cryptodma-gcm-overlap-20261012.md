# gcm(aes) with CryptoDMA and GHASH overlapped — 2026-10-12

`sg2002_aes_probe` registers `sg2002-gcm-aes`, a `gcm(aes)` AEAD at priority
400. That is above the `gcm` template over the engine's `ctr(aes)`, so users of
`gcm(aes)` get it, and so does `rfc4106(gcm(aes))` for IPsec ESP.

## Why

The template runs its two halves one after the other. It sends a CTR request
to the engine, and the CPU sleeps in the status poll while the engine
encrypts. Then the CPU hashes the whole message. With the engine at about
50 MB/s and GHASH on the CPU, the CPU is idle for a large share of each
request.

## How

Each chunk of up to 4 KiB (the engine's bounce buffer) goes through these
steps:

1. Copy the chunk into the bounce buffer and start the engine
   (`crypto_start_locked()`).
2. While the engine runs, hash what is ready, reading only the request's
   scatterlists:
   - the associated data, on the first chunk;
   - for encryption, the ciphertext of the previous chunk;
   - for decryption, the ciphertext of this chunk, which is the input.
3. Wait for the engine (`crypto_finish_locked()`) and copy the result out.

E(J0) for the tag comes from the first chunk, as in the template: 16 zero
bytes in front of the data, counter J0. H is computed once per key, by the
engine in ECB mode. GHASH is the `lib/crypto` one, so it uses the C906
assembly from patch 0041. `crypto_run_locked()` is now `crypto_start_locked()`
followed by `crypto_finish_locked()`, and the existing ciphers, hashes and
ioctls are unchanged.

## Results

Stand, SG2002, `7.2.10-nanokvm-os-r1` with its generic GHASH (about 15 MB/s),
the server idle. The test module is `platform/modules/sg2002-aes/test/`.

- 3,000 random cases against the software `gcm(aes)`: AES-128, 192 and 256,
  associated data 0 to 600 bytes, messages 0 to 12 KiB including chunk
  boundaries, tags of 4 to 16 bytes, in place and out of place. Encryption and
  decryption matched, and a flipped bit in the associated data, ciphertext or
  tag was rejected each time. 0 failures.

| bytes | template, encrypt (MB/s) | template, decrypt (MB/s) | driver, encrypt (MB/s) | driver, decrypt (MB/s) |
| ---: | ---: | ---: | ---: | ---: |
| 64 | 2 | 1 | 2 | 2 |
| 512 | 7 | 7 | 8 | 8 |
| 1420 | 9 | 8 | 10 | 11 |
| 4096 | 10 | 10 | 11 | 12 |
| 16384 | 10 | 10 | 12 | 12 |
| 65536 | 10 | 10 | 12 | 13 |

The engine's time is now almost entirely hidden behind GHASH, so GHASH alone
sets the rate. Encryption of a message in one chunk (up to 4,080 bytes) can
only overlap the associated data, since its ciphertext comes out of that
chunk. Decryption overlaps every chunk.

## With the vector GHASH

After a reboot of the stand (r1 kernel), one boot measured three states in
turn, with the server running (about 15 % system time). First the stock
modules. Then `libgf128hash.ko` and `gcm.ko` from the vector patch, built
against r1. Then also this `sg2002_aes_probe.ko`. The original modules were
loaded back afterwards.

- Before the measurements, with the patched library: 5,000 random GHASH
  cases through the scalar function, the vector path and `ghash_update()`,
  and 1,250 with bottom halves disabled, 0 failures. A preemption run of 475
  calls with 136 involuntary switches had 0 mismatches. The driver test ran
  3,000 cases with 0 failures.
- Library GHASH: 17 MB/s stock, 46-49 MB/s patched.

| bytes | stock template (MB/s) | template + vector GHASH (MB/s) | driver + vector GHASH, encrypt (MB/s) | driver + vector GHASH, decrypt (MB/s) |
| ---: | ---: | ---: | ---: | ---: |
| 64 | 2 | 2 | 2 | 3 |
| 512 | 8 | 10 | 11 | 11 |
| 1420 | 11 | 19 | 18 | 20 |
| 4096 | 12 | 22 | 22 | 28 |
| 16384 | 12 | 24 | 30 | 33 |
| 65536 | 13 | 24 | 33 | 33 |

`gcm(aes)` goes from 11 to 18-20 MB/s at 1420 bytes and from 13 to 33 MB/s
at 64 KiB. Below about 4 KiB the driver gains little over the template with
the same GHASH: a message in one chunk overlaps only its associated data on
encryption.

## Not covered

- The r3 Image itself was not booted. The modules above are the same sources,
  built against r1.
- The engine mutex is now held during the GHASH of a chunk, up to about
  0.1 ms with the vector GHASH. Hardware SRTP requests wait that long behind a
  `gcm(aes)` request.
