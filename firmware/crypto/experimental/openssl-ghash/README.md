# OpenSSL C906 GHASH qualification

`0010-ghash-c906.patch` uses the shared C906 five-block GHASH generator under
its BSD-3-Clause license. Four vector lanes and one scalar block process a
prefix; OpenSSL retains remaining blocks, AAD, padding, lengths and tags.
The vector path starts at 3072 bytes. Short inputs and single-block
multiplication retain upstream code. FIPS and no-assembly paths are unchanged.

Acceleration requires `NANOKVM_OPENSSL_GHASH=auto`, the all-online-CPU
XTheadVector result from OpenSSL's startup `riscv_hwprobe`, and permission
for the current thread on every eligible call. Unknown/missing options,
unsupported hardware, disabled vectors and errno-returning query failures
retain software. The image downloader opts in for its curl child and preserves
explicit operator overrides. Scalar XTheadBb is part of the base C906 profile.

The explicit opt-in is necessary: a private native OpenSSH listener proved
that its preauth filter kills the per-call `prctl` on large AES-GCM packets.
The default path now avoids that syscall, without changing SSH's sandbox.
ChaCha's musl environment lookup was separately corrected in its parent PR.
Fatal seccomp policies cannot be made safe by waiting for errno from a forbidden
syscall; clients must qualify their policy before opting in.

## Current physical measurements

OpenSSL 3.5.9, GCC 16.2, restricted C906/O3 flags, physical 1 GHz SG2002 with
video active. Three rotated trials used the same binary, equal-length option
values and per-process disabled ASLR. Input offset is 5. Values are MiB per
process CPU-second. `validation.json` pins source, patches and binaries;
`native-results.jsonl` contains all samples and `native-medians.csv` the medians.

| Operation | Bytes | Generic, DMA off | C906, DMA off | Generic, DMA on | C906, DMA on |
| --- | ---: | ---: | ---: | ---: | ---: |
| GHASH | 1024 | 28.563 | 28.521 | 28.537 | 28.607 |
| GHASH | 3072 | 28.559 | 39.727 | 28.616 | 39.685 |
| GHASH | 16384 | 28.576 | 42.654 | 28.593 | 42.673 |
| AES-128-GCM open | 16384 | 10.019 | 10.737 | 14.313 | 14.631 |
| AES-256-GCM open | 16384 | 8.254 | 8.770 | 13.637 | 14.074 |

At 16 KiB GHASH alone gains about 49%; full software AES-128/256-GCM gains
7.2%/6.2%. With the parent's AES DMA enabled, full-GCM gains are 2.2%/3.2%.
No small-record gain or HTTPS throughput gain is claimed. These results replace
the earlier CPU-info/broad-ISA measurements. An intermediate restricted-ISA
build accidentally excluded the integration through a compiler macro; the
compile-guard regression and actual-entry tests below catch that failure.

The expanded key and powers live only in a temporary large-call frame.
`GCM128_CONTEXT` remains 448 bytes and the AES provider context 984 bytes;
256 initialized EVP contexts add 324 KiB anonymous memory in all 12 runs.
There is no context growth or persistent key cache. Expansion and assembly
frames total 1376 bytes plus callers. See `native-memory.txt` for raw readings.

## Verification

- The shared generator passed 200 independent bit-serial cases per primitive;
  126 vector encodings matched GNU binutils.
- ASan/UBSan exercises the production dispatcher. Negative controls remove
  capability, current-thread and opt-in gates. Preprocessing tests prove
  integration with the scalar baseline ISA and reject the old vector-macro gate.
- `verify-path.c` checks 32 boundaries/alignment cases against test-only atomic
  counters at the actual assembly entry: 12 hits with opt-in, zero with generic,
  thread vectors off, or default policy under a fatal syscall filter.
  Production retains 5808 dynamic definitions and no counter exports.
- Native DMA on/off, generic and vector-off profiles each passed 408 AEAD,
  2160 extended GCM, 288 CTR-carry and 864 SHA streaming/copy cases. GCM's
  independent ciphertext/tag fingerprint is `a2d7088437b53472`.
- A separate loopback OpenSSH listener, with preauth seccomp active, handled
  short and 5011-byte authentication requests over AES-128/256-GCM and ChaCha20.
  All six returned the expected authentication-failure packet without a crash.
  The tests disabled source penalties only in that private listener.
- Installed curl verified five 16 MiB Ubuntu ISO range downloads: AES-128/256-GCM
  with TLS 1.2/1.3 and ChaCha20 with TLS 1.3. CA validation, length and SHA-256
  passed. No installed device library or service was replaced.

`bench-ghash.c` isolates GHASH through the public low-level AAD API.
`bench-gcm.c NAME SECONDS` measures warm AEAD records; `bench-key.c NAME`
measures new keys and 256 live contexts. Synthetic fixed keys/nonces are used
only by these test programs.
