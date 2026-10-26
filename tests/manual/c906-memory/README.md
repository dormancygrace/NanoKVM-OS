# C906 kernel memmove qualification

This module calls the running kernel's `__memmove`. It does not link or replace
the assembly implementation. The target is NanoKVM's SG2002/C906, with native
misaligned scalar accesses enabled by its platform firmware.

## Build and run

Set `KSRC`, `KBUILD` and `CROSS` to the source, output and cross-compiler prefix
of the kernel being tested. Put `OUT` under the task's disposable host build
directory, outside the source checkout.

~~~sh
mkdir -p "$OUT"
cp tests/manual/c906-memory/{Kbuild,driver.c,memory_test.h} "$OUT/"
make -C "$KSRC" O="$KBUILD" ARCH=riscv CROSS_COMPILE="$CROSS" M="$OUT" modules
~~~

Copy `c906_memory_test.ko` to device tmpfs. On the C906, as root:

~~~sh
insmod ./c906_memory_test.ko seed=151396358
rmmod c906_memory_test
insmod ./c906_memory_test.ko seed=23130
rmmod c906_memory_test
dmesg | tail
~~~

Insertion validates **46,440 cases per seed** and prints PASS. A mismatch rejects
insertion and logs FAIL. The test checks the actual unmapped end guards; an
out-of-bounds implementation can fault the kernel, so use a temporary test boot.
Remove the helper afterwards.

Coverage includes dense lengths 0–129 and boundaries through 16 KiB; every 8×8
source/destination alignment; both address orders; equal pointers; positive and
negative overlap distances through 129 bytes; the returned destination pointer;
unchanged bytes outside the destination; guarded source/destination page ends in
both address orders; and overlaps in both directions ending at the guard.

## Measurements

The C906-only implementation handles 0/1 byte at entry, copies 64-byte blocks in
the safe direction, then words and a bounded 2/4-byte head/tail. It loads each
overlapping block before any store and uses no vector or floating-point state.
The same standalone code serves the normal and early-boot PI aliases.

Qualification results and measurement limits are recorded in
[results.md](results.md). The first usercopy rewrite was rejected after syscall
regressions. The final PR includes a different, layout-preserving usercopy patch
0053; its [qualification](../c906-usercopy/results.md) records 172,352 checks
and the final pipe/socket controls. Existing memcpy remains unchanged.


## Integration timing control

Build the ordinary pipe benchmark with the NanoKVM cross compiler's configured
C906 ISA and tuning defaults:

~~~sh
"$CROSS"gcc -O2 -Wall -Wextra -Werror -static \
    -o "$OUT/pipe" tests/manual/c906-memory/pipe.c
~~~

Run it as root from device tmpfs with no test module loaded. Keep the CPU
frequency and video workload unchanged between boots:

~~~sh
./pipe > pipe.csv
~~~

Each CSV contains nine repetitions of seven sizes (16 bytes through 32 KiB)
with two source/destination alignments. Each iteration writes then reads the
same pipe; its capacity is checked before starting. Batches transfer up to
64 MiB, with 2,048–65,536 iterations. Payload and adjacent destination guards
must match after every batch. Both process CPU time and wall time are recorded.

Run two series per kernel, with a stock boot before and after the candidate.
Compare per-case median time divided by the number of iterations. Large pipe
transfers are split into page-sized kernel copies. This is an integration
regression control for the complete read/write path, not a direct memmove
benchmark. Preserve the kernel build IDs, configuration, compiler version,
clock frequency and raw CSVs with the measurements.
