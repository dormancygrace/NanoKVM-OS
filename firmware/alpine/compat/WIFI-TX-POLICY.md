# AIC8800 TX startup profile

NanoKVM OS ships `nanokvm-wifi-tx-policy` in `/usr/sbin`. The image enables no
override by default. The tool manages only
`/etc/modprobe.d/nanokvm-wifi-tx.conf` and never loads/unloads modules or changes
the scheduling of running threads.

```sh
nanokvm-wifi-tx-policy status
nanokvm-wifi-tx-policy normal
nanokvm-wifi-tx-policy driver-default
```

`normal` writes `options aic8800_fdrv bustx_thread_prio=0` atomically. It takes
effect on the next module load through `modprobe`, including the
`S25wifimod` startup path. Other existing module/kernel-command-line options can
affect the final parameter list; inspect the actual `modprobe -D aic8800_fdrv`
output. BusyBox `-D` displays dependencies and options without loading modules.

`driver-default` removes this tool's option file, leaving other configuration
alone. A file without the ownership marker is preserved with an error. The
status command reports the managed profile and, on a live system, the loaded
module parameter. That parameter is not a measurement of current thread policy:
changing it through sysfs does not reschedule an existing TX thread.

The normal profile affects TX only. It does not change RX, the server's Go
execution capacity, bitrate, codec, or SRTP settings. Removing the file also
does not revert threads that have already started; it selects the configuration
for a future module load.

For an offline image root:

```sh
NANOKVM_SYSROOT=/absolute/image/root nanokvm-wifi-tx-policy normal
```

The base package installs the tool (platform/packages.list). It does not select
`normal` automatically. The compatibility/vendor init script remains unchanged.

In live trials `normal` raised encrypted H.264 over Wi-Fi to about 30 FPS at
10 Mb/s for one viewer. A full boot with the option set is not yet tested.
