# Reassert LT6911C MIPI pinmux at capture probe

Sipeed's [SG200x LT6911 probe](https://github.com/sipeed/MaixCDK/blob/fff52193aea22d58b2ef289fbc65482792559c75/components/3rd_party/sophgo-middleware/sophgo-middleware/v2/component/isp/sensor/sg200x/lontium_lt6911/lt6911_sensor_ctl.c)
writes 3 to every four-byte word from 0x0300116c through 0x03001190.
These are the ten SG2002 MIPIRX pad mux registers.

Our Device Tree already sets the same values when the CIF driver binds.
The sensor probe now repeats them for C, at the existing D initialization point.
The MPI startup order is StartSensor, StartDev, StartMIPI, SensorProbe, then
VI pipe creation/start. Reassertion therefore happens after CSI configuration
and before capture starts. Each new capture probe repeats the writes; periodic
receiver measurement does not.

D keeps the same ten commands and the existing 50-microsecond delay. UXC,
UE and missing/unrecognized model metadata do not gain pinmux writes. The
existing ID checks, receiver session locks and cleanup are retained. Any failed
pinmux command aborts probe before I2C initialization or identification.

The [Issue #8 D fix](https://github.com/dormancygrace/NanoKVM-OS/issues/8#issuecomment-5824862720)
changed both the pinmux and incorrect D identification. Its successful result
does not isolate a pinmux defect. No later overwrite of the C mux has been
demonstrated; this restores a stock initialization step and remains an
unconfirmed experiment for the Issue #63 C artifacts.

## Verification

Run python3 tests/test-lt6911c-pinmux.py and repeat with NANOKVM_SANITIZE=1.
The offline test applies every production sensor-control patch to a pinned,
unmodified upstream fixture, then compiles and executes the complete resulting
probe with mocked hardware access. It verifies all ten addresses/values,
repeated probes, both C/D paths, failure at each command (40 cases), initialization
failure/retry, model gates, and ordering before I2C access. The complete MMF
wrapper is also cross-built against the patched sensor sources.

Physical C qualification is pending; the local stand has UXC. Compare this PR
alone against the same source, cable and input mode, then evaluate the separate
C/D stock-clock PR independently. Receiver firmware, EDID, clock policy,
polling and CSI timing parameters are unchanged here.
