# C906 zram LZ4 benchmark

`zram-bench.c` times writes and reads of a secondary zram device with the LZ4
backend and checks every page byte for byte. Use
`python3 tests/test-lz4-compressor.py` for the compression API contract.

Cross-build it as a static target executable. Coordinate an exclusive slot,
create a secondary device with `/sys/class/zram-control/hot_add`, select LZ4
and size it to 8,388,608 bytes. Supply its actual device number:

```sh
zram-bench /dev/zramN 2048 > zram.csv
zram-bench /dev/zramN 2048 periodic-kat > zram-periodic.csv
```

The client refuses zram0, non-ZRAM devices and non-block files, and verifies
the sysfs device identity. The normal mode makes 21 timed repetitions. The
finite `periodic-kat` mode adds periods 2 to 256 bytes (powers of two),
including forward overlapping copies at distance eight. Both modes check the
full data and allocation guards. Reset and remove the secondary device even if
testing fails. Never change the production zram0 device.
