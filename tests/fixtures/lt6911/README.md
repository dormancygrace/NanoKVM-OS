# LT6911 sensor-control test input

The test downloads the unmodified upstream file from sophgo/SensorSupportList at
[e2824e1d07f516c41238e123fefbab71a34843bc](https://github.com/sophgo/SensorSupportList/blob/e2824e1d07f516c41238e123fefbab71a34843bc/sensor/cv182x/lontium_lt6911/lt6911_sensor_ctl.c),
the sensors revision in `platform/sources.lock`. The vendor source is not
redistributed in this tree; see `docs/DISTRIBUTION.md`.

`tests/test-lt6911-probe.py` verifies both its pinned SHA-256 and Git blob
identity before applying all production sensor-control patches. Network or
checksum failures fail the test, including CI. To run offline, download the file
outside the source checkout and set `NANOKVM_LT6911_SOURCE` to its path; the same
checksum checks apply. The download is reused within one test process.

Vendor headers are replaced by minimal test declarations. The test executes the
complete C/D/UXC probe and its error paths without hardware access.
