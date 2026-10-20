# Logs

The web interface (Settings → System → Diagnostics) shows three logs: the
system log (syslog), the kernel log and the application log of the
NanoKVM server, for the current boot and the two before it.

**Live logs are in RAM.** syslogd writes `/run/log/messages` on the tmpfs
`/run`, rotating it at 512 KiB into `messages.0` (at most 1 MiB in all).
The kernel log is the kernel's ring buffer and the application log a 1 MiB
buffer in the server. Nothing of these is written to the SD card directly.

**The archive is on the SD card.** Once a minute the server appends the new
lines of each log, sanitized, to `/var/log/nanokvm-boots`:

| | |
|---|---|
| Boots kept | the current one, the previous and the one before (`current`, `previous`, `older`) |
| Size | at most 2 MiB a log and boot (18 MiB in all), rewritten from the newest 1 MiB when a file reaches 2 MiB |
| Writes | the new lines once a minute, synced; a boot record (`boot.json`) after each |

A new boot moves `current` to `previous` and `previous` to `older`; the
oldest is deleted.

An image installed before this setup keeps its `/etc/conf.d/syslog`
unless it is Alpine's default (`SYSLOGD_OPTS="-t"`). The package update
then moves syslog to `/run/log` and removes the old `/var/log/messages*`.
On an image whose syslog still writes `/var/log`, the server reads the
system log there.
