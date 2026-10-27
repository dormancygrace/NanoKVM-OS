# OpenSSL C906 GHASH qualification

`0010-ghash-c906.patch` uses the shared C906 five-block GHASH generator under
its BSD-3-Clause license. Four vector lanes and one scalar block process a
prefix; the remaining 0–4 whole blocks reuse the already expanded scalar key.
OpenSSL retains AAD, padding, lengths, tags and the operation boundaries.
The vector path starts at 3072 bytes. Short inputs and single-block
multiplication retain upstream code. FIPS and no-assembly paths are unchanged.

Acceleration requires `NANOKVM_OPENSSL_GHASH=auto`, the all-online-CPU
XTheadVector result from OpenSSL's startup `riscv_hwprobe`, and permission
for the current thread on every eligible call. Unknown/missing options,
unsupported hardware, disabled vectors and errno-returning query failures
retain software. The image downloader opts in for its curl child and preserves
explicit operator overrides. Scalar XTheadBb is part of the base C906 profile.

This does not make all OpenSSL GHASH constant-time. The upstream data-indexed
4-bit tables remain in every single-block `gmult` (including tag work), inputs
under 3072 B, and processes without the opt-in or vector permission. Only an
eligible call's complete blocks use the constant-time C906 arithmetic, now
including its scalar remainder. The curl downloader first requests ChaCha;
GHASH acceleration therefore matters on its second, generic cipher attempt.

The explicit opt-in is necessary: a private native OpenSSH listener proved
that its preauth filter kills the per-call `prctl` on large AES-GCM packets.
The default path now avoids that syscall, without changing SSH's sandbox.
ChaCha's musl environment lookup was separately corrected in its parent PR.
Fatal seccomp policies cannot be made safe by waiting for errno from a forbidden
syscall; clients must qualify their policy before opting in.

## Recorded physical measurements

OpenSSL 3.5.9, GCC 16.2, restricted C906/O3 flags, physical 1 GHz SG2002 with
video active, with the selected AES extraction/tail changes from #511, before
the SHA-1/DES/TDES scope cleanup. GHASH assembly, AES algorithms and thresholds
are unchanged, but these are measurements of the recorded pre-cleanup library,
not new timing results from the restacked build.
Three rotated trials used the same binary, equal-length option
values and per-process disabled ASLR. Input offset is 5. Values are MiB per
process CPU-second. `validation.json` pins source, patches and binaries;
`native-results.jsonl` contains all samples and `native-medians.csv` the medians.

| Operation | Bytes | Generic, DMA off | C906, DMA off | Generic, DMA on | C906, DMA on |
| --- | ---: | ---: | ---: | ---: | ---: |
| GHASH | 1024 | 28.568 | 28.625 | 28.577 | 28.518 |
| GHASH | 3072 | 28.638 | 39.710 | 28.638 | 39.766 |
| GHASH | 16384 | 28.550 | 42.785 | 28.649 | 42.788 |
| AES-128-GCM open | 1400 | 11.165 | 10.941 | 11.013 | 11.057 |
| AES-256-GCM seal | 1400 | 9.337 | 9.210 | 9.332 | 9.288 |
| AES-128-GCM open | 16384 | 11.256 | 12.376 | 14.079 | 14.564 |
| AES-256-GCM open | 16384 | 9.489 | 10.266 | 13.730 | 14.238 |

At 16 KiB GHASH alone gains about 49%; full software AES-128/256-GCM gains
10.0%/8.2%. With the parent's AES DMA enabled, full-GCM gains are 3.4%/3.7%.
At 1400 B, the software-only AES-128 open / AES-256 seal medians differ by
−2.0% / −1.4%, within the recorded trial spread; a material small-record
regression was not established. No small-record or HTTPS throughput gain is
claimed. These results replace
the earlier CPU-info/broad-ISA measurements. An intermediate restricted-ISA
build accidentally excluded the integration through a compiler macro; the
compile-guard regression and actual-entry tests below catch that failure.

The expanded key and powers live only in a temporary large-call frame.
`GCM128_CONTEXT` remains 448 bytes and the AES provider context 984 bytes;
256 initialized EVP contexts add 324 KiB anonymous memory in all 12 runs.
There is no context growth or persistent key cache. Every eligible call rebuilds
the key (four scalar multiplies plus the 576-byte vector table) and queries
thread permission once. In the normal `GHASH_CHUNK` loop this repeats per 3 KiB.
That setup is included in the reported throughput; it trades repeated work for
unchanged context size. Expansion and assembly
frames total 1376 bytes plus callers. See `native-memory.txt` for raw readings.

## Verification

The cleaned branch is stacked on AES #511, which now derives directly from
SHA-256 #508 without SHA-1 offload commits. `scope-cleanup-validation.json`
records the zero-fuzz patch application, clean C906/O3 cross-build, unchanged
5808 public exports, generator/dispatcher negative controls and downloader
tests. That cleanup kept the GHASH patch byte-identical. The subsequent scalar-tail
revision changes the glue, so those historical qualifications retain their
original identities and are not measurements of the new tail behavior.

- The shared generator passed 200 independent bit-serial cases per primitive;
  126 vector encodings matched GNU binutils.
- ASan/UBSan exercises the production dispatcher. Negative controls remove
  capability, current-thread and opt-in gates. Preprocessing tests prove
  integration with the scalar baseline ISA and reject the old vector-macro gate.
- `verify-path.c` checks 32 boundaries/alignment cases against test-only atomic
  counters at the actual assembly entry: 12 hits with opt-in, zero with generic,
  thread vectors off, or default policy under a fatal syscall filter.
  That test-library qualification is pinned separately in `validation.json`;
  the GHASH patch is unchanged in the selected-parent integration run.
  Production retains 5808 dynamic definitions and no counter exports.
- Native DMA on/off, generic and vector-off profiles each passed 408 AEAD,
  2160 extended GCM, 288 CTR-carry, 864 SHA streaming/copy and 20,736 AES
  tail/output/IV cases. GCM's
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

## Scalar remainder regression tests

`test-arithmetic.py --source APPLIED_OPENSSL --negative-control` executes the
actual glue and generated assembly against an independent bit-serial GHASH,
anchored by the NIST zero-key GCM example. All 3520 cases cover 40 keys
(including zero, all ones and H=1), 11 lengths, 8 offsets, nonzero initial Xi,
all 0–4-block remainders, and input/context/key-table guards. Deliberate tail
omission, tail offset, key-layout, Xi-byte-order and expected-digest faults
must fail. The complete patch stack and real pinned OpenSSL headers are used
by `tests/test-openssl-source.py` in fast CI.

QEMU cannot execute XTheadVector 0.7.1. The arithmetic test explicitly maps a
small whitelist of its vector operations to RVV 1.0, using VLEN 128; the scalar
XTheadBb code is unchanged. This validates arithmetic and integration, not the
original vector instruction encoding. `generate-ghash.py --check` separately
verifies the production encodings with GNU binutils. `test-arithmetic.c` also
supports a native build with `-DGHASH_NATIVE`, the original assembly, real
`secure_getenv`/`prctl` and `NANOKVM_OPENSSL_GHASH=auto`.

The scalar-tail revision also passes those same 3520 arithmetic cases on the
physical C906 with the original XTheadVector words and real per-thread gate.
A clean full C906/O3 build preserves all 5808 public exports. Against that
library, generic, vector/software-AES and vector/DMA profiles each pass 972 AES,
2160 GCM, 288 CTR, 20,736 tail/output/IV and 864 SHA checks. Another 28 actual
EVP SHA-224/256 answers cover 4 KiB plus continuation tails with DMA on/off
against Python hashlib. Exact patch/library/helper identities and outputs are
in `scalar-tail-validation.json`. These are correctness checks; the historical
throughput table above was not remeasured for this revision.
