# Ethernet receive batching

The integrated SG2002 Ethernet MAC can spend a substantial part of the C906
CPU budget handling interrupts during browser ISO transfers. At Ethernet
startup, S30eth applies a small software coalescing profile through
/usr/libexec/nanokvm/ethernet-rx-batching:

| Parameter on eth0 | Value |
| --- | ---: |
| gro_flush_timeout | 200000 ns (200 microseconds) |
| napi_defer_hard_irqs | 2 consecutive empty polls |

NAPI uses a timer to poll again before re-enabling hardware interrupts, then
returns to interrupts after two empty polls. This is not continuous busy
polling. See the [Linux NAPI documentation](https://docs.kernel.org/networking/napi.html#software-irq-coalescing).

This is a policy for the whole physical eth0 interface, including traffic
carried through an Ethernet VLAN. It is not limited to browser ISO sessions.
Wi-Fi, USB networking and VLAN child interfaces are not modified. The helper
requires the exact Device Tree compatible string sophgo,cv1800b-dwmac and both
sysfs controls; unsupported interfaces/kernels are skipped.

Hardware RX/TX coalescing, ring sizes, offload feature switches, qdiscs and IP
configuration are unchanged. No ethtool package or new daemon is required.

## Startup and failures

The helper runs after Ethernet is brought up, on both boot and subsequent
S30eth starts. It only changes the kernel-default pair 0/0. Existing custom
values, including a partially configured pair, are preserved. Reapplying an
already active profile does not write it again.

Writes are read back. If applying the second control or verifying the result
fails, the helper attempts to restore both original values and reports failure;
restoration failures are reported too. S30eth logs the tuning failure and
continues normal address/DHCP setup. Missing optional controls do not prevent
Ethernet from starting. Disabling Ethernet skips the helper.

To leave this profile disabled after the next boot, add this exported variable
to /etc/conf.d/nanokvm-network:

    export NANOKVM_ETH_RX_BATCHING=0

That flag skips applying the policy; it does not undo settings already active
in the running kernel. To restore the default pair immediately, as root:

    printf '0\n' > /sys/class/net/eth0/napi_defer_hard_irqs
    printf '0\n' > /sys/class/net/eth0/gro_flush_timeout

The helper can then be run directly to reapply the policy. For diagnostics,
NANOKVM_RX_NET_CLASS_DIR selects a private sysfs fixture, and
NANOKVM_ETH_RX_TUNER overrides the helper path used by S30eth.

## Measured benefit and tradeoff

SG2002/C906 at 1 GHz, 100 Mb/s full-duplex Ethernet, Chrome Main with video on.
Both sides use the same production browser ISO reader from commit
41da3f2874db6e9f58b029f90e129b7cde55abd9. Three 32 MiB reads per profile,
matching offsets, rotated profile order and fresh mounted sessions; Windows
file caching is bypassed and only the NBD cache is flushed. SHA-256 is disabled
during throughput measurements.

| Median of three runs | Default network | Software profile |
| --- | ---: | ---: |
| Read rate, MiB/s | 8.495 | 9.000 |
| CPU busy | 94.4% | 92.7% |
| Encoded video frames/s | 49.8 | 51.7 |
| Ethernet interrupts/s | 3312 | 696 |

Samples in MiB/s: default 8.495 / 8.517 / 8.432; proposed
8.612 / 9.000 / 9.042. Median paired gain is 5.7%; 9.000 MiB/s is 9.437 MB/s.
These three runs on one device do not establish the same gain for every workload.
Encoded FPS measures the device encoder, not browser rendering latency.

The latency tradeoff is visible during a saturated ISO read. In the same fresh
session series, the median of each run's p95 Windows echo-call duration rises
from 35.6 to 43.5 ms. Echo probes run on Windows, waiting 50 ms after each reply or timeout before
sending the next request. The measured call duration includes host scheduling.
Three of 144 baseline probes and six of 120 proposed-profile probes exceeded
the 250 ms timeout; there were no Ethernet RX error/drop counter increments.

A separate three-repeat, single-session comparison, with drained prefetch
between samples, records Windows' integer-millisecond kernel RTT as well.
Median per-run p50/p95 values are 23/34 ms for defaults and 29/36 ms for the
200 microsecond profile. It also observes timeouts in both configurations
(5/157 baseline, 2/127 proposed). The tail varies between series; the profile
does not promise unchanged interactive latency under saturation. These echo
probes are not an end-to-end keyboard or mouse latency measurement.

With video running and no ISO reads, three six-second windows per profile give
median echo-call p50 0.655/0.654 ms and p95 0.769/0.781 ms (default/proposed),
with no echo timeouts. Encoded frame rate remains about 59 FPS.

A separate mixed-read test, three repeats of an 8 MiB stream followed
immediately by a 4 KiB seek and a 1 MiB read elsewhere, gives:

| Median | Default network | Software profile |
| --- | ---: | ---: |
| Total read time | 1.421 s | 1.362 s |
| Immediate 4 KiB seek | 103.4 ms | 96.2 ms |
| Following 1 MiB read | 257.5 ms | 284.3 ms |

The complete sequence is faster, while its individual reads do not all improve.
A full Windows installation and end-to-end HID response were not benchmarked.

## Alternatives and verification

Changing hardware RX coalescing from 137 to 217 microseconds on top of this
profile gave 8.983 MiB/s median in the three-profile comparison, versus
9.000 MiB/s with only the software policy. Its incremental benefit was
inconsistent, so the hardware default is retained.

A software timer of 100 microseconds kept kernel RTT closer to the baseline
but gave little sustained benefit in the follow-up comparison:
8.715 MiB/s versus 8.642 default and 9.153 with 200 microseconds. Those results
belong to that separate latency series, not the fresh-session speed table.

The shipped helper was applied and reapplied on the device with read-back.
A full 256 MiB SHA-256 read and six selected/repeated/EOF range checks passed.
Hashing runs on Windows and is separate from throughput timing. Kernel,
installed server and firmware were not replaced by this networking change.

Host tests exercise compatible/unsupported interfaces, opt-out, existing
policies, idempotence, actual write failures, read-back mismatch and rollback,
normal/disabled/VLAN Ethernet starts, optional-helper failure, and package
placement. They run in the regular shell-test CI set.
