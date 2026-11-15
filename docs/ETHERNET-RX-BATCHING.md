# Ethernet receive batching

Interrupt handling for the integrated SG2002 Ethernet MAC can take a large part
of the C906 CPU during sustained transfers such as browser ISO reads. An
optional software interrupt-coalescing profile reduces that load. It is
**disabled by default**.

When enabled, Ethernet startup runs `/usr/libexec/nanokvm/ethernet-rx-batching`,
which sets on eth0:

| Parameter on eth0 | Value |
| --- | ---: |
| gro_flush_timeout | 200000 ns (200 microseconds) |
| napi_defer_hard_irqs | 2 consecutive empty polls |

NAPI then polls again on a timer before re-enabling hardware interrupts and
returns to interrupts after two empty polls. This is not continuous busy
polling. See the [Linux NAPI documentation](https://docs.kernel.org/networking/napi.html#software-irq-coalescing).

## Trade-off

During a saturated 100 Mbit/s ISO read the profile gives a few percent more
throughput and far fewer Ethernet interrupts, but round-trip latency under that
load rises (p95 by several milliseconds) and is not guaranteed. Without heavy
traffic, latency is unchanged.

## Scope

The profile applies to the whole physical eth0 interface, including traffic on
an Ethernet VLAN; it is not limited to ISO sessions. Wi-Fi, USB networking and
VLAN child interfaces are not modified. The helper acts only when eth0 has the
Device Tree compatible string `sophgo,cv1800b-dwmac` and both sysfs controls
exist; otherwise it does nothing.

Hardware RX/TX coalescing, ring sizes, offloads, qdiscs and IP configuration are
unchanged. No ethtool package or daemon is required.

## Enable and disable

To apply the profile from the next boot, add this to
`/etc/conf.d/nanokvm-network`:

    export NANOKVM_ETH_RX_BATCHING=1

Remove the line or set it to `0` to stop applying the profile. This does not
undo settings already active in the running kernel. To restore the kernel
defaults immediately, as root:

    printf '0\n' > /sys/class/net/eth0/napi_defer_hard_irqs
    printf '0\n' > /sys/class/net/eth0/gro_flush_timeout

Running the helper with `NANOKVM_ETH_RX_BATCHING=1` reapplies the profile.

## Startup and failures

The helper runs after eth0 is brought up, on boot and on every later S30eth
start. Disabling Ethernet skips it. It changes only the kernel-default pair
0/0: existing custom values, including a partially configured pair, are kept,
and an already active profile is not written again.

Writes are read back. If the second write or the read-back fails, the helper
restores both original values and reports the failure (and any restore
failure). S30eth logs the failure and continues normal address and DHCP setup.

## Environment variables

| Variable | Use |
| --- | --- |
| `NANOKVM_ETH_RX_BATCHING` | `1` enables the profile; default `0` |
| `NANOKVM_ETH_RX_TUNER` | Helper path used by S30eth |
| `NANOKVM_RX_NET_CLASS_DIR` | Replaces `/sys/class/net` for diagnostics with a sysfs fixture |
