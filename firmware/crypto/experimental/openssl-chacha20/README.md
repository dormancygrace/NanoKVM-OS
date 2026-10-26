# OpenSSL C906 ChaCha20 qualification

Candidate: OpenSSL 3.5.9 LTS with the `0001-chacha20-c906.patch` primitive.
Control: the same source, compiler and C906/O3 profile from PR #504, without
custom crypto. Go and kernel measurements are not substituted for this control.

`verify.c` compares raw EVP ChaCha20 against a separate RFC 8439 scalar
construction. Its 1680 cases cover offsets 0–15, lengths around the 64/320-byte
boundaries through 64 KiB, low-word and 64-bit counter rollover, split updates,
in-place output, guards and context copies. `--native` also forks/execs with
vector state disabled and tests concurrent threads with different vector
controls. Existing `../openssl-c906/evp-bench.c 0` supplies AEAD known-answer,
modified-tag/ciphertext rejection and boundary checks.

`bench.c` reuses the baseline TLS-record benchmark at 64, 128, 256, 319, 320,
512, 640, 768, 1024, 1400, 4096 and 16384 bytes, with the actual five-byte TLS
buffer offset. It reports process CPU time, elapsed time and peak RSS; fixed
keys/nonces are exclusively synthetic benchmark inputs. Run rotating baseline,
candidate and candidate-with-`NANOKVM_OPENSSL_CHACHA20=generic` trials on the
physical C906. Do not use emulator throughput to select a path or threshold.

Current validation: cross-build without warnings; all 5808 versioned libcrypto
exports unchanged; generated assembly matches the kernel generator and its
140 distinct vector encodings match GNU binutils; the differential test harness
passes against host OpenSSL. Physical C906 correctness, threshold selection,
TLS 1.2/1.3 curl, memory and concurrent-video measurements are pending.
The initial 320-byte threshold is a candidate, not a measured recommendation.

Native C906 first pass: all 1680 cases and per-thread vector-off/on checks pass. Three rotated warm-key AEAD trials measured about 47.35 versus 29.50 MiB/CPU-s for 16 KiB records. A 320-byte vector dispatch lost to software; the candidate threshold is now 640 bytes. The dispatcher checks length before entering the C906 helper, avoiding its stack/setup work for short calls, including the Poly1305-key block. Final measurements of this revised boundary are pending.
