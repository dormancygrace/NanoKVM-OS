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
cp "$repo"/tests/manual/c906-usercopy/{Kbuild,driver.c,usercopy_test.h,client.c} "$testout/"
make -C "$out/kernel/src" O="$out/kernel/build" ARCH=riscv \
    CROSS_COMPILE="$cross" M="$testout" modules
"${cross}gcc" -O2 -Wall -Wextra -Werror -static "$testout/client.c" -o "$testout/client"
```

Copy only these two outputs to the test device's RAM-backed /tmp directory.
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

No test module is installed in the production image. Restore the normal kernel
boot and remove only the temporary test files after qualification.
