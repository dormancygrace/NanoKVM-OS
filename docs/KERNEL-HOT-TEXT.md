# Hot kernel text

The kernel places the code of its frequent paths together at the start of
`.text`, in a fixed order, instead of where the link order happens to put
it. Kernel patch `0088` and the list `platform/kernel/hot-text.lds`.

## Why

The C906 L1 instruction cache is 32 KiB: two ways of 16 KiB with 64-byte
lines. The code of a system call, a wakeup or a context switch is spread over
the 7 MB of kernel text, and its lines share cache sets and evict each other.
Which ones collide depends on everything linked before them, so any change
anywhere in the kernel moved its hot paths by tens of percent:

- 4 KiB of padding in `init/main.c`: `stat()` 6 % faster.
- `-mbranch-cost=1` left the instruction counts unchanged but doubled the
  I-cache misses of a pipe round trip (114 to 216 per round trip, 46 % more
  time) and cut those of `stat()` from 10 to 3 (18–28 % less time).

With the hot code in a fixed order, its placement no longer depends on the
rest of the kernel: after the same 4 KiB padding none of the listed functions,
nor the entry code before them, moved.

## Layout

At the start of `.text` (`arch/riscv/kernel/vmlinux.lds.S`):

1. the exception entry (`.irqentry.text`: `handle_exception`,
   `ret_from_exception`, `__switch_to`) and the noinstr text (system call and
   interrupt entry);
2. the scheduler core: `kernel/sched/core.o`'s `.sched.text` (`__schedule`,
   `schedule`), then the rest of `.sched.text`, which keeps its bounds;
3. the listed functions, starting at the cache set where the scheduler core
   ends: the functions shared by the system-call paths, then each path's own
   functions (pipe, read, write, stat, open, getppid, page fault, Ethernet and
   Wi-Fi send and receive), so that a path is the shared block plus one run.
   The shared block and the pipe path have a spare line after each function;
4. a slot of one I-cache way (16 KiB) for the functions of `stat()` of a
   missing name (the slow lookup and the negative dentry), at a measured
   offset in the slot. Being a whole way, the slot leaves the cache sets of the
   code after it unchanged.

Each listed function starts at a fixed offset from the first one
(`. = MAX(., __hot_list + OFFSET);`, written by `scripts/kernel-hot-text/pin.py`
from a measured kernel). The order alone does not hold the layout: when a call
becomes short enough for linker relaxation, the function shrinks and every one
after it moves by a line, and the measured placement of every later path with
it. With the offsets, a shorter function leaves a gap; a longer one pushes the next one,
and the layout needs measuring again. All of the hot text is kept (`KEEP`)
when the linker discards unreferenced code, for the same reason: three
functions of the noinstr text that nothing calls moved the list by five
lines, and `open()` became 25 % slower.

Every built-in function is compiled into its own section and starts on a
64-byte line. The line alignment comes from the linker script (`SUBALIGN(64)`)
instead of `-falign-functions=64`: with relaxation, GCC's per-function
`.align` leaves its NOPs at the start of each function section (ld does not
remove them there), which made the image 700 KiB larger. Modules keep
`-falign-functions=64` from the CPU profile. The image is 33 KiB smaller than
before.

## Data

The C906 D-cache is 64 KiB, four ways of 16 KiB. The system call and network
paths read the system call table, the file, socket and protocol operations,
`init_net` and the hash tables on every call, so the cache sets of the data
matter as much as those of the code. Patch `0089` fixes them:

- The read-only data starts at offset 0x0000 in the way, the tables after the
  kallsyms tables in `.rodata` at 0x0380, and `.bss` at 0x1000: the offsets
  of the measured kernel. Without them the read-only data moves with the size
  of the init sections, the operation tables with the number of functions
  (kallsyms), and `.bss` with everything before it.
- `.data` starts with `init_thread_union`, aligned to 16 KiB, and needs no
  offset.
- The data, read-only data and bss of every linked object are kept when
  unreferenced sections are discarded, so nothing inside them moves either.

What moving them cost, with the code at its offsets: trimmed exports moved the
read-only data 12 KiB and a `stat()` call went from 0.1 to 2.9 D-cache read
misses; the data of discarded objects moved `init_net` and `tcp_hashinfo` by
4 KiB and Ethernet TCP send had 5 % more I-cache, 10 % more D-cache and 15 %
more jTLB misses. With `.data` and `.bss` back in place but the read-only data
still elsewhere, `open()` went from 0.1 to 4.3 D-cache read misses: what
counts is where the data lies against each other, not one section alone.

## Unused code and exports

The kernel is linked with `--gc-sections` (`CONFIG_LD_DEAD_CODE_DATA_ELIMINATION`)
and exports only the symbols its modules use (`CONFIG_TRIM_UNUSED_KSYMS`). The
out-of-tree modules are built after the kernel, so the exports they import are
listed in `platform/kernel/ksyms-keep.txt` (`scripts/kernel-ksyms/generate.sh`);
a module that needs one missing from the list fails its modpost. The code is
compiled without `-fdata-sections` (patch `0090`): with it GCC 16 generates
different code for functions that address static data, and even with every hot
function at its offset a pipe round trip took 17 % more cycles (I-cache misses
81 to 99). The kernel image is 10.75 MB → 9.37 MB (the boot image 7.25 → 6.72 MB)
and the system has 884 KB more memory (MemTotal).

The same paths, release kernel, against `main` (pathbench, three passes per
boot; cycles and I-cache misses per call from the C906 PMU):

| Path | main | unused code discarded |
| --- | ---: | ---: |
| `stat()`, 1 / 4 / 8 path components | 3.31 / 4.01 / 4.99 µs | 3.34 / 4.07 / 5.01 µs |
| `open()` + `close()`, 1 / 4 / 8 components | 6.55 / 7.41 / 8.31 µs | 6.47 / 7.50 / 8.42 µs |
| `stat()` of a missing name | 5.85 µs | 5.89 µs |
| pipe round trip | 28,700 cycles, 81 I-misses | 29,900 cycles, 89 I-misses |

The pipe round trip of `main` itself took 28,700–31,100 cycles over three
boots. Ethernet TCP send and UDP receive (100 Mbit/s, C906 counters over the
whole transfer) have the same I-cache, D-cache and jTLB misses as `main`.

Without the offsets and the slot, the same discarding made `stat()` 12–50 %
and `open()` 11–25 % slower.

## Effect

Release kernel, NanoKVM PCIe, the same image booted twice, against `main`
booted three times (time per call; CPU counters from the C906 PMU):

| Path | main | hot text | I-cache misses per call |
| --- | ---: | ---: | --- |
| pipe round trip between two processes | 34.2 µs | 27.3 µs (−20 %) | 116 → 93 |
| `stat()`, 1 / 4 / 8 path components | 6.0 / 6.7 / 7.6 µs | 3.3 / 4.0 / 4.9 µs (−44 / −40 / −35 %) | 13 → 0.6 |
| `open()` + `close()`, 1 / 4 / 8 components | 9.3 / 9.5 / 10.5 µs | 6.5 / 7.3 / 8.3 µs (−30 / −23 / −21 %) | 27 → 12 |
| `stat()` of a missing name | 8.3 µs | 5.9 µs (−29 %) | |
| `getppid()` | 0.42 µs | 0.40 µs (−6 %) | 0 |
| anonymous page fault | 20.9 µs | 20.2 µs (−4 %) | |
| fork + wait | 587 µs | 577 µs (−2 %) | |

Instruction counts are unchanged; the time comes from I-cache misses (about
180 ns each).

Whole workloads, four boots of each kernel alternated (results beyond the
spread between the boots of the same kernel):

| Workload | main | hot text |
| --- | ---: | ---: |
| WireGuard between two network namespaces on the device, TCP | 78.0 Mbit/s | 81.8 Mbit/s (+5 %) |
| the same at 20 Mbit/s, CPU per MB | 163 ms | 157 ms (−3.5 %) |
| Ethernet receive at 40 Mbit/s, CPU per MB | 36.0 ms | 32.2 ms (−10 %) |
| zram read | 106 MB/s | 108 MB/s (+2 %) |

Within the spread, no change: TCP over Ethernet at line rate (94.9 Mbit/s;
CPU per MB +3 %, spread 6 %), WireGuard to the PC over the product's tunnel,
the web UI's cold load, SSH backup and upload (upload +5 %, spread 7 %),
Wi-Fi, SD card and filesystem tests.

The H.264 stream to a browser over Wi-Fi, with the same deterministic
animation on the captured screen for every boot (60 fps, 2.63 MB/s), three
boots each: 33.5 % CPU on main, 33.8 % with the hot text; the server process
used 116.5 and 118 ms per MB (+1.3 %). Its own kernel paths are not in the
list: appending them moved the earlier functions through linker relaxation
(shorter calls) and cost the pipe, open and fork paths far more than the
1.6 % it saved on the stream.

## Changing the list

The list is measured, not derived: a pipe round trip moved by 15 % when a few
functions of the spaced part changed. To make a new one for another kernel
version:

1. Build and boot the debug kernel (`NANOKVM_KERNEL_PROFILE=debug`; it has the
   function profiler).
2. On the device, with `scripts/kernel-hot-text/paths.c` built statically:
   `profile.sh /path/to/paths OUTDIR` for the system-call paths, and
   `profile.sh - OUTDIR idle 22` with nothing running, then
   `profile.sh - OUTDIR ethtx 22` (and `ethrx`, `wifitx`, `wifirx`) while a PC
   sends or receives 12 MiB over that link.
3. On the build host, with a release kernel build that has patch 0088:
   `scripts/kernel-hot-text/generate.py --kbuild BUILD/kernel/build
   --profiles OUTDIR --cross PREFIX --out platform/kernel/hot-text.lds`.
4. Boot the release kernel with the new list and compare the paths above with
   the old list (the I-cache miss counts need the PMU events of the debug
   kernel or a counting module) before committing it.
5. Pin the measured layout: `scripts/kernel-hot-text/pin.py --kbuild
   BUILD/kernel/build --cross PREFIX --list platform/kernel/hot-text.lds --out
   platform/kernel/hot-text.lds`, with the build of the kernel that was measured.
   Keep the slot at the end of the old list.

Names the kernel no longer has match nothing and are ignored.
