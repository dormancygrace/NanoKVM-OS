# C906 usercopy qualification results

**Superseded in kernel r8.** Patch 0053 below was replaced by upstream's
fast-unaligned usercopy (0058/0059). Measured 2026-10-31 on the stand, r7
(0053) against the same tree with upstream's copy, two boots each, tmpfs
pread/pwrite at every user and kernel offset: misaligned copies 13-50% faster
at 512 B to 64 KiB in both directions, aligned and short copies the same or
faster; `client --conservative` passes all 172352 checks on r8 (residual at
most 63 bytes above exact), the strict check fails as expected. The 0053
record follows.

Measured on the SG2002/C906 NanoKVM PCIe stand on 2026-10-25. The selected
patch accelerates the scalar misaligned-source path. The earlier rewrite of
all scalar paths is excluded: it did not consistently preserve short-copy
performance.

## Selected implementation

Patch 0053 replaces aligned loads plus shift/or reconstruction with native
misaligned scalar loads and aligned stores. It leaves the normal short-copy
path, destination alignment and aligned 64-byte loop intact. It uses no
vector or floating-point state and is qualified for this SG2002/C906 target.

The replacement occupies the original 44-byte region. Its two word-access
exception entries and one byte-retry entry preserve the previous entry count.
Comparison of the two complete kernel images found only **40 different bytes
in 7,415,382 bytes of .text**, all within that region. Every other text byte
and all named text symbol addresses are unchanged. In particular, the normal
short and aligned instructions are byte-identical at the same addresses.

Exception-table targets deliberately change. A fault in a word copy retries
its accessible prefix bytewise; the aligned bulk fixup first restores the
original end pointer. The existing tail still controls SUM cleanup and the
uncopied-byte return value. This returns an exact accessible prefix, including
when an unaligned load crosses into an inaccessible page.

The fixed region size is intentional for the qualified RV64 compressed
instruction build. Repeat the binary-layout comparison when changing this
assembly, compiler or kernel configuration; it is not a general performance
guarantee for other configurations or processors.

## Method

Baseline: previous scalar-memmove revision
ab5f866fe6e62d67f2c44f67914f9b993b306ae1 with its original usercopy.
Candidate: that same tree plus patch 0053. Both use Linux
7.3.0-nanokvm-os-r3, the same GCC 16.2 toolchain and kernel configuration.
The CPU stayed at 1 GHz; video encoding and its Ethernet viewer remained
active. USB functions were off. Each boot settled for at least 60 seconds.

The transfer helper measures a write/read pair through a pipe or a local Unix
stream socket. It covers 35 sizes from 0 through 32768 bytes and two user
buffer layouts: aligned source/destination offsets 16/16, or misaligned 17/19.
These names describe the user buffers; kernel buffer allocation is not fixed.

Each cell has three repetitions in each of three separate benchmark
processes. First take the median within each process, then the median of the
three process medians. Each batch uses 16 MiB / length write/read pairs,
bounded to 1024–32768 pairs. The helper verifies payload and adjacent guards.

ASLR is disabled only for each benchmark process with setarch -R. System
settings and the video server are unchanged. The same static executable is
used throughout. The correctness module is unloaded during timing.
CPU time is CLOCK_PROCESS_CPUTIME_ID for the benchmark, including its syscalls;
it is not total system CPU time, video CPU usage or an isolated assembly
instruction measurement. Wall time is recorded separately.

[medians.csv](medians.csv) contains every cell, all three process medians, the
final medians and changes. Positive change means more CPU time (slower).
CPU and wall units are nanoseconds per write/read pair.

## Main comparison

| Length | Pipe aligned | Pipe misaligned | Socket aligned | Socket misaligned |
| --- | ---: | ---: | ---: | ---: |
| 16 B | +0.47% | -0.24% | -0.58% | -0.68% |
| 128 B | +0.27% | -1.76% | +0.07% | -1.40% |
| 1400 B | +0.06% | -11.68% | -1.99% | -5.23% |
| 2920 B | -0.85% | -16.93% | +9.78% | -0.48% |
| 3776 B | +0.27% | -18.48% | +8.86% | -1.76% |
| 4 KiB | -1.09% | -18.41% | +0.73% | -6.19% |
| 16 KiB | -2.09% | -15.02% | -0.49% | -14.29% |
| 32 KiB | -2.49% | -15.68% | +2.49% | -8.57% |

Across all cases of 128 bytes or less, the largest positive CPU-time change
was +0.72% for pipes and +0.50% for sockets. The earlier 13–19% short-copy
penalties did not recur with this selected implementation.

The clearest gains are misaligned pipes: about 18% less CPU at 4 KiB and
15–16% at 16–32 KiB. Misaligned sockets also improve, but their allocation
and active-video variability are larger.

## Controls and remaining variability

The main comparison has unfavorable aligned socket medians at 2920 bytes
(+9.78%) and 3776 bytes (+8.86%). These results are retained in the table and
CSV. Individual process ranges overlap. The normal aligned assembly path is
identical, but that alone does not prove identical complete syscall timing.

After the candidate series, the baseline was booted again and the complete
matrix repeated once, with three repetitions per cell. Its results are the
baseline_after columns in medians.csv. This extra control is separate from
the three-process main comparison.

A further targeted baseline/candidate boot pair tested the two disputed
socket sizes at eight aligned source/destination placements (offsets
16 + 64 * index), with 4096 write/read pairs per batch. Each placement again
has three repetitions in each of three processes. The source is transfer.c
built with -DUSERCOPY_LAYOUT; adding this optional build mode left the
ordinary transfer executable byte-identical.

[layout-medians.csv](layout-medians.csv) retains every placement and all
three process medians. Candidate CPU time decreased at all eight placements:
2.76–5.80% for 2920 bytes and 2.54–6.22% for 3776 bytes. The initial socket
slowdowns therefore did not reproduce in this control. The control does not
establish a specific cache or allocator cause, nor a guaranteed gain in these
aligned socket cases. The supported conclusion is that the narrow candidate
removes the observed short-copy regression and benefits misaligned transfers,
with visible run-to-run variation in larger socket tests.

Earlier all-path rewrites, padding experiments and ASLR-enabled pilot runs
are not pooled into these results. Both code layout and execution environment
varied in those pilots, so they cannot isolate a single cause of the original
slowdown. No browser-ISO throughput, video FPS or whole-system improvement is
claimed by this qualification.

## Correctness and build checks

The manual verifier calls the live kernel, not private copies of its
assembly. All **172,352 checks passed**, including **86,240 fault cases**:

- Raw, SUM-enabled and public wrappers, in both directions.
- Every 8-by-8 alignment and dense short lengths through larger boundaries.
- Inaccessible/read-only pages, exact accessible prefixes and residuals.
- Destination guards, public copy_from_user zero fill and SUM ownership.
- Successful copies bounded by inaccessible pages, including zero length.

The negative control corrupts one captured result byte and fails with
FAIL payload as expected. No new kernel fault/oops messages appeared.
Both complete kernel Image/modules builds passed; patch application with
zero fuzz reproduces the compiled candidate source. Platform configuration,
build plumbing, CPU-profile and kernel-configuration checks passed.

## Artifact identity

| Artifact | Identity |
| --- | --- |
| Baseline GNU build ID | f5c5684091fe7cc40d98e424dbfb0f66e42b24d5 |
| Candidate GNU build ID | d93d1b9c279bcc825dee3e490f9e3fcfa77b8bd3 |
| Candidate uaccess.S SHA-256 | cbdb5c71ea28d342e3ec7415205613e3fe6667d6e18d2bf4c6089391942d2df6 |
| Kernel configuration SHA-256 | 681af49f9a6e3b93d24a2e2e0f1c09a42499d8e9f672e470c639819236d3e2df |
| Candidate temporary FIT SHA-256 | e8db4f3d329a9ad9d751739a56428ea4a6e20a1024a49e44ebfb71d728e7f2e9 |
| Ordinary transfer executable SHA-256 | ef246c235f5bfc4657e2c68509a0c16c8aea839778f34405b06d4318a3e5fc02 |
| Layout control executable SHA-256 | ab4f70683a2f800737ad6a463d469d74b92d43bd85fc77c221551b1be4d68e8e |

Raw per-repetition CSVs, boot/environment logs, manifests and binary proofs
are retained locally under .workspace/tasks/c906-gcc-recon with the usercopy-
prefix. The public CSVs preserve the process-level observations used in this
report. See README.md for rebuilding the verifier and benchmark.

The stand was restored to its original installed kernel after qualification.
All six tracked installed-file hashes matched their pre-test values, Ethernet
video continued advancing, USB functions were off, and temporary FIT and RAM
helpers were removed. No on-device backup copies were created.
