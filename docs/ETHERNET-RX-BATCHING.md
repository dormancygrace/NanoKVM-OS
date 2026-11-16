# Ethernet interrupt coalescing

The integrated SG2002 Ethernet MAC (Synopsys GMAC 3.70a, 100 Mbit/s through
the internal PHY) raised an interrupt for almost every packet: for every
TCP acknowledgement while the device sends video or files, and for every
data packet while it receives an image. Each one cost the C906 an interrupt,
a softirq and a NAPI poll. Hardware coalescing does not help: its receive
watchdog tops out at about 200 microseconds, shorter than the gap between
acknowledgements, and the MAC has no TSO.

Ethernet startup (S30eth) therefore sets software interrupt coalescing on eth0:

| Parameter on eth0 | Value |
| --- | ---: |
| napi_defer_hard_irqs | 2 |
| gro_flush_timeout | 1000000 ns (1 ms) |

While packets keep arriving, NAPI polls again on a 1 ms timer instead of
re-enabling the interrupt, and returns to interrupts after two empty polls.
An idle link answers the first packet at once, as before. See the
[Linux NAPI documentation](https://docs.kernel.org/networking/napi.html#software-irq-coalescing).

## Effect

TCP bulk transfers between a PC and the device over Ethernet, 60 MiB in
64 KiB writes; CPU is the whole device's busy time per MiB.

| Direction | Without | With |
| --- | --- | --- |
| Device sends | 94.5 Mbit/s, 80 ms/MiB | 94.7 Mbit/s, 56 ms/MiB |
| Device receives | 81 Mbit/s (CPU-bound), 105 ms/MiB | 94.7 Mbit/s, 31 ms/MiB |

Ping from the PC while the device sends is unchanged. A packet that arrives
during a transfer waits at most 1 ms for the next poll. While the device
receives at the full 100 Mbit/s, pings queue behind the transfer on the
sending side, as on any saturated link.

## Scope and turning it off

The setting covers the whole physical eth0, including traffic on an Ethernet
VLAN. Wi-Fi and USB networking do not use NAPI and are not affected.

To return to an interrupt per packet until the next Ethernet start, as root:

    printf '0\n' > /sys/class/net/eth0/napi_defer_hard_irqs
    printf '0\n' > /sys/class/net/eth0/gro_flush_timeout
