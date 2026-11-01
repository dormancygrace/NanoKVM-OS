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
