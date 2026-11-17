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
   The shared block and the pipe path have a spare line after each function.

Every built-in function is compiled into its own section and starts on a
64-byte line. The line alignment comes from the linker script (`SUBALIGN(64)`)
instead of `-falign-functions=64`: with relaxation, GCC's per-function
`.align` leaves its NOPs at the start of each function section (ld does not
remove them there), which made the image 700 KiB larger. Modules keep
`-falign-functions=64` from the CPU profile. The image is 33 KiB smaller than
before.

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

Names the kernel no longer has match nothing and are ignored.
