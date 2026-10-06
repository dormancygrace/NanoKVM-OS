# C906 LZ4 qualification

These tools generate and test scalar candidates from a supplied Linux tree.
They build one temporary module against the exact running kernel ABI; they do
not replace the kernel, ZRAM, compression policy, or production dispatch.
Generated Linux sources, modules, binaries and measurements belong outside the
repository. Only the generator, client and probe sources are published here.

The probe compares the actual kernel exports with an unchanged private copy
and these variants:

| ID | Candidate |
| --- | --- |
| 0 | Running kernel `LZ4_compress_fast` / `LZ4_decompress_safe` |
| 1 | Unchanged namespaced source |
| 2 | Aligned native read and copy fast paths |
| 3 | Two-word bounded match counting |
| 4 | Two ordered copies per wild-copy iteration |
| 5 | Match counting plus two-copy loop |
| 6 | All of 2–4 |
| 7 | Independently aligned load and store in copy8 |
| 8 | Variant 7 plus match counting and two-copy loop |
| 9 | Aligned wild-copy dispatch once per span |
| 10 | Variant 9 plus two-copy fallback loop |

Forward store order and the existing seven-byte wild-copy overrun contract are
preserved. Match counting never reads beyond its original input bound. All
candidates retain the supplied kernel's source licenses and namespace its
public helpers, including the non-exported `LZ4_resetStream` global.

Build with paths to the source, completed kernel build, the production cross
compiler and exact production KCFLAGS:

```sh
python3 tools/c906-lz4/build.py --kernel KERNEL_SOURCE \
  --kernel-build KERNEL_BUILD --output PRIVATE_FRESH_DIRECTORY \
  --cross CROSS_COMPILER_PREFIX --cflags 'EXACT_PRODUCTION_KCFLAGS'
```

Use a coordinated hardware window, load the existing LZ4 modules as necessary,
and load the new probe. The misc device is root-only, `/dev/c906-lz4` (0600).
Requests operate solely on generated data within private allocated buffers;
there is no production compression hook. The ioctl validates sizes, offsets,
variant, operation and iteration count. Buffers are released when the client
closes; scheduling points sit outside the timed function call.

```sh
insmod PRIVATE_FRESH_DIRECTORY/c906_lz4.ko
PRIVATE_FRESH_DIRECTORY/lz4-client /dev/c906-lz4 qualify
PRIVATE_FRESH_DIRECTORY/lz4-client /dev/c906-lz4 bench > PRIVATE_FULL.csv
PRIVATE_FRESH_DIRECTORY/lz4-client /dev/c906-lz4 page-bench > PRIVATE_PAGES.csv
rmmod c906_lz4
python3 tools/c906-lz4/analyze.py PRIVATE_PAGES.csv --output PRIVATE_SUMMARY.json
```

`qualify` covers 45,056 valid-input cases: 32 lengths (0–64 KiB), 11 variants,
eight patterns, eight offsets, compression and decompression. It verifies
compressed byte identity, decoded bytes, return sizes and guards.
`bench` alternates candidate order over seven repetitions, eight sizes, eight
patterns and three offsets (29,568 rows). `page-bench` focuses on aligned 4 KiB
pages with 21 repetitions and 128 calls per sample (3,696 rows). Compression
uses acceleration 1, matching the default current ZRAM LZ4 backend. That
backend uses a mutex; the probe retains ordinary process-context preemption.

The timing column records wall time around each codec call. IRQs, preemption
and other hardware activity are included. Compare against both ID 0 and ID 1,
and inspect per-pattern regressions and the unchanged-code control before
accepting small differences. A fast result for one repeated pattern does not
establish a useful change for mixed ZRAM pages. Run real ZRAM acceptance only
after choosing a stable candidate, then build the whole kernel once.

## Selected production change

Patch `platform/kernel/0039-c906-aligned-lz4-decompression.patch` selects
variant 9 only in the decompressor. The shared helper's compressor instantiation
retains its original code. The Kconfig option defaults off for other kernels;
NanoKVM's `platform/kernel/config` enables it, and `selected.config` is available
for a separate qualification tree. The production GCC optimization remains
`-O3`, with compiler vectorization disabled. This helper uses scalar aligned
64-bit loads and stores; it requires no vector context.

The C906 qualification passed all 45,056 cases before a complete kernel/module
build. Across the 3,696-row page matrix, paired median decompression time fell
17.9% against the actual kernel export and 19.1% against the unchanged private
copy. Pattern-dependent changes and IRQ noise were substantial; small gains
are not sufficient evidence to select a candidate. The compressor's emitted
text was byte-identical before and after the selected patch.

A separate real-ZRAM comparison used an 8 MiB secondary LZ4 device, 2,048
mixed pages and 21 write/read repetitions. Every read was compared byte for
byte. Median read wall time fell from 112.725 ms to 92.643 ms (17.8%); process
CPU time fell from 67.676 ms to 57.925 ms (14.4%). Write wall time rose 4.3%,
while write CPU time fell 1.2%; the unchanged compressor and concurrent video
activity limit interpretation of that difference. These are finite generated
page measurements, not a claim about application latency or every workload.

### Real secondary-ZRAM acceptance

Cross-build `zram-bench.c` as a static target executable. Coordinate an exclusive
slot, create a secondary device with `/sys/class/zram-control/hot_add`, select
LZ4 and size it to 8,388,608 bytes. Supply its actual device number:

```sh
PRIVATE/zram-bench /dev/zramN 2048 > PRIVATE/zram.csv
PRIVATE/zram-bench /dev/zramN 2048 periodic-kat > PRIVATE/zram-periodic.csv
```

The client refuses zram0, non-ZRAM devices and non-block files; it verifies the
sysfs device identity. The normal mode makes 21 timed repetitions. The finite
`periodic-kat` mode adds valid periods 2, 4, 8, 16, 32, 64, 128 and 256 bytes,
including forward overlapping copies at distance eight. Both modes check the
full data and allocation guards. Reset and remove the secondary device in a
cleanup handler even if testing fails. Never change the production zram0
device or publish generated data, modules, executables or raw measurements.
