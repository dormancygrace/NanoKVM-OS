# C906 scalar memmove qualification

Qualification date: 2026-10-25. Target: NanoKVM SG2002/C906 at 1 GHz, using
the production kernel configuration and GCC 16.2.0 (Buildroot 2026.08).
Video stayed enabled over Ethernet throughout the boot comparisons.

## Accepted change

Patch `0052-riscv-native-scalar-memmove.patch` replaces the kernel's scalar
memmove body with a direct C906 implementation. It uses native misaligned
scalar accesses, 64-byte blocks, 8-byte words and bounded 2/4-byte head/tail
accesses. Each overlapping block is loaded before any store. Zero/one-byte
copies have a short entry aligned to 32 bytes. The return pointer and
early-boot PI aliases are preserved.

The implementation is specific to the only supported CPU. It has no runtime
CPU dispatch, stack frame, vector state or floating-point state.

## Correctness and integration

- The final production Image and modules build passed. The configuration is
  byte-identical to the stock test kernel; compiler and build identity match.
- The patch applies with zero fuzz to the prepared baseline and produces the
  exact source used for the final image.
- The public test module calls the real running kernel's `__memmove`.
  Two seeds passed **68,208 cases**, including dense lengths 0–129, boundaries
  through 16 KiB, every 8×8 alignment, both address orders, equal pointers,
  positive/negative overlaps, return values, untouched bytes, and unmapped
  source/destination end guards.
- A negative control with an altered expected byte rejected module insertion
  at the first case. It did not change the production implementation.
- Platform configuration/build, CPU-profile and kernel-configuration checks
  passed, including the kernel-config negative cases.
- The final image booted through the ordinary release initramfs. The installed
  bootloader, stock kernel and applications were untouched; temporary boot and
  test files were removed after testing, and six installed-file hashes matched.

The exact runtime GNU build IDs were:

| Kernel | GNU build ID |
| --- | --- |
| Stock, before and after | `bb1f7b1a02f66e3c4c3a5bbca8d68ef010dec03e` |
| Final memmove only | `f5c5684091fe7cc40d98e424dbfb0f66e42b24d5` |
| Excluded memmove + usercopy candidate | `b3e88dc94db48dfa22a7413eddeb7164463f8e5a` |

## Function timing

Separate private assembly copies were measured with 21 repetitions per cell,
alternating execution order, identical-code controls, and a reversed link
layout. For each placement, per-cell medians were combined across identical
baseline/candidate copies. The table gives the observed range across tested
alignment/overlap cases and both layouts, not a confidence interval.

| Copy size | Reduction in measured cycles |
| --- | --- |
| 7 bytes | 29–36% |
| 8 bytes | 28–40% |
| 64 bytes | 31–55% |
| 4 KiB | 35–50% |
| 16 KiB | 33–50% |

No memmove cell had a positive time delta in both-layout analysis. Zero and
one-byte cases were included, but their timings are sensitive to call overhead
and code placement. Identical-code controls showed substantial address noise;
the small-case percentages are not an application-level speed claim.

The final aligned implementation has the same 330 function bytes as the
measured body (SHA-256
`a58dd7475161fb752352af09ac03c73eb3a1dbed41c75822100880e9671acff8`).
Its entry position was then checked in the final image, and that image received
the live correctness and pipe tests above.

## Ordinary pipe regression control

The included `pipe.c` performs a write/read pair through a normal pipe with
no test module loaded. Each kernel received two series of nine repetitions
per case. A batch transfers up to 64 MiB, with 2,048–65,536 iterations.
Process CPU time and wall time are recorded; payload and adjacent guards
are checked. Stock boots bracketed the candidate boots.

The table reports the median CPU nanoseconds per write/read pair.
Alignment 0 uses source/destination offsets 16/16; alignment 1 uses 17/19.
The percentage is relative to the mean of the two stock-boot medians.

| Bytes | Alignment | Stock before, ns | Stock after, ns | Memmove only, ns | Change |
| --- | --- | --- | --- | --- | --- |
| 16 | 0 | 2855.6 | 2876.0 | 2859.4 | -0.22% |
| 16 | 1 | 2857.6 | 2863.5 | 2864.6 | +0.14% |
| 64 | 0 | 3342.9 | 3365.5 | 3345.4 | -0.26% |
| 64 | 1 | 3340.0 | 3388.5 | 3348.3 | -0.47% |
| 128 | 0 | 2761.9 | 2777.8 | 2765.6 | -0.15% |
| 128 | 1 | 3003.5 | 3015.3 | 3007.5 | -0.06% |
| 1400 | 0 | 4135.2 | 4168.4 | 4132.8 | -0.46% |
| 1400 | 1 | 5638.3 | 5681.9 | 5658.0 | -0.04% |
| 4096 | 0 | 5204.5 | 5805.2 | 5215.3 | -5.26% |
| 4096 | 1 | 11242.3 | 11625.9 | 11263.6 | -1.49% |
| 16384 | 0 | 25954.9 | 24247.7 | 24629.6 | -1.88% |
| 16384 | 1 | 50549.2 | 49198.8 | 50223.1 | +0.70% |
| 32768 | 0 | 100941.1 | 98708.1 | 101290.1 | +1.47% |
| 32768 | 1 | 132503.9 | 135181.9 | 133257.4 | -0.44% |

No consistent regression was observed for the memmove-only image. Results were
inside the stock-to-stock range or less than 0.6% above its slower median.
Large-transfer variability is visible in the stock runs themselves. This
integration control does not establish a whole-system or video throughput gain.

## Usercopy decision and measurement limits

The separate native usercopy candidate passed 155,744 checks, including
86,240 fault cases and raw/SUM/public wrapper checks. It nevertheless failed
the ordinary pipe timing guard: 16-byte pairs took about 13–14% more CPU time,
and 128-byte pairs about 11–19% more, while several large unaligned cases
improved. That first rewrite is excluded. The final PR includes the subsequent
layout-preserving patch 0053, qualified separately with 172,352 checks and
pipe/socket controls in [the usercopy results](../c906-usercopy/results.md).

An earlier benchmark through a temporary module was rejected for performance
attribution: repeating the same kernel image and helper produced up to 70%
variation in short calls. Those timings are not evidence for either acceptance
or rejection here. The ordinary pipe measurements supersede them; the module's
independent correctness checks remain valid.

The measurements on this page isolate kernel memmove and the rejected first
usercopy candidate. They do not describe the final usercopy patch 0053. Existing
optimized memcpy, Go runtime routines and compiler flags are unchanged.

## Guard coverage extension

The verifier now runs 46,440 cases per seed. Added cases mirror both guard-end
copies between the allocations and place overlapping copies in both directions
at each allocation end. The 68,208 live-kernel result above belongs to the
original 34,104-case verifier; it is not a claim that the extended matrix was
run on hardware.
