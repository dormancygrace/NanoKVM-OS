# C906 usercopy qualification

This manual test exercises the running SG2002 kernel's
__asm_copy_to_user / __asm_copy_from_user, their SUM-enabled entries, and the
public copy_to_user / copy_from_user wrappers. It does not link private copies
of the assembly into the test module.

The verifier covers both directions, every 8-by-8 alignment, dense short
lengths, larger boundaries through 32 KiB, inaccessible/read-only pages,
accessible prefixes, destination guards, zero padding and SUM ownership.
Successful copies are also tested between inaccessible guard pages, including
zero-length operations at an inaccessible address. The candidate must return
the exact uncopied suffix after a fault; this is stricter than accepting a
conservative residual from a generic implementation.

Build against the same kernel configuration and symbol versions as the
temporary kernel being tested. Keep generated files outside the source tree:

```sh
repo=/home/dgrace/src/NanoKVM-OS
out=/home/dgrace/build/nanokvm-os/current
cross="$out/buildroot-output/host/bin/riscv64-buildroot-linux-musl-"
testout="$out/manual-usercopy"
mkdir -p "$testout"
cp "$repo"/tests/manual/c906-usercopy/{Kbuild,driver.c,usercopy_test.h,client.c,transfer.c} "$testout/"
make -C "$out/kernel/src" O="$out/kernel/build" ARCH=riscv \
    CROSS_COMPILE="$cross" M="$testout" modules
"${cross}gcc" -O2 -Wall -Wextra -Werror -static "$testout/client.c" -o "$testout/client"
"${cross}gcc" -O2 -Wall -Wextra -Werror -static "$testout/transfer.c" -o "$testout/transfer"
```

Copy only these three outputs to the test device's RAM-backed /tmp directory.
Run the correctness test on a temporary kernel boot:

```sh
insmod /tmp/c906-gcc-recon/c906_usercopy_test.ko
/tmp/c906-gcc-recon/client
rmmod c906_usercopy_test
```

A successful run reports 172352 usercopy checks, including 86240 fault cases.

Since kernel r8 the usercopy is upstream's (patches 0058/0059, Linux 7.4
commits 396d73f7b3d4 and 30514aa420fd, replacing 0053): with misaligned access
found fast at boot it copies 8-word blocks with misaligned loads and stores.
Patch 0071 sends a fault in such a block to a bytewise replay from the block
start, as 0053 did, so the residual stays exact. The client also checks that
nothing past the reported copied prefix was stored, in both directions
(raw_copy_*_user leaves everything past it unmodified); without 0071 that
check fails as soon as a block stores a word before its fault.
With the module loaded, running client --negative-control must fail with
FAIL payload: it deliberately corrupts one byte of the result captured by the userspace
verifier. It does not modify the kernel implementation.

After unloading the module, run the integration controls:

```sh
setarch riscv64 -R /tmp/c906-gcc-recon/transfer pipe > pipe.csv
setarch riscv64 -R /tmp/c906-gcc-recon/transfer socket > socket.csv
```

Each command records three repetitions of 35 lengths (0–32768 bytes), with
aligned and misaligned buffers: 210 rows. It validates the final payload and
both adjacent destination guards for every batch. socket uses a local Unix
stream socket pair. Pipe capacity is 64 KiB; the Unix send buffer is sized so
the bounded write can finish before its paired read.

Run each command in three separate processes. First take the median of the
three repetitions per cell, then the median of the three process medians.
The setarch option disables ASLR only in these benchmark processes; it does
not change the system or the video server. Keep wall time as a separate measure. Alternate the
baseline/candidate boots, use the same test binary, wait at least 60 seconds
after each boot, and keep the CPU at 1 GHz and Ethernet video active.
Repeated boots are important because larger buffers have visible variability
between allocations. Do not run the correctness module during timing.

For the targeted buffer-placement control in the results, compile the same
source with -DUSERCOPY_LAYOUT, then run its socket mode in three separate
processes with setarch -R. It tests 2920 and 3776 bytes at eight aligned user
buffer placements (source and destination offsets 16 + 64 * offset_index),
with 4096 write/read pairs per batch and three repetitions per placement.
Its CSV uses offset_index in place of alignment.

The smaller seven-size pipe control from ../c906-memory/pipe.c is useful as an
additional check, but it does not replace this broader matrix. Code placement
and run-to-run variability can change small benchmark results. The production
patch therefore leaves the normal short and aligned instruction paths
byte-identical, and changes only the old unaligned shift-copy region.

See [results.md](results.md) for the qualified image, measurements and code
placement controls. No benchmark helper or test module is installed in the
production image. Restore the normal kernel boot and remove only the temporary
test files after qualification.
