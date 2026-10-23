# LT6911C/D firmware evidence for diagnostics

This is a static analysis of published images, not physical NanoKVM C/D
qualification. No chip image is installed or distributed by this change.
The host continues to depend on the board's internal MCU firmware.

## Sources and identity

- [C reverse project, commit 6c4d1880](https://github.com/RichardAntalik/LQ079L1SX01-LT6911C-board-reverse-engineering/tree/6c4d1880fd2fb3a92bf779070866ed2be9460fdb/firmware):
  compare `LT_6911_firmware.c` with `firmware.hexdump`, rather than trusting
  recovered function names. The author explicitly calls the reverse incomplete.
  The dump contains 10,007 bytes, beginning `02 1f 39`.
  Decoded-dump SHA-256:
  `c4675ca95aafaba94d686464ea9ac237c61c61b15a3f38cf9c7b560144c63e55`.
- [Espressif D image, commit b413578f](https://github.com/espressif/esp-video-components/blob/b413578f9186a814f7df2bd04442e796c978fae3/esp_cam_sensor/sensors/lt6911/fw/LT6911D_v1_default.hex):
  2,215 data records and one EOF record, all Intel HEX checksums valid.
  Contiguous decoded bytes cover 0000..7CE4 (31,973 bytes).
  Decoded-image SHA-256:
  `201a0ea30c062eec2fc2ffbc139c688a0b393d7c66eca83d1dc26004ca9c1acf`.
  HEX-file SHA-256:
  `3189c9c09df54f06e84efce47ccd2ba925b17218bf442f8b9eb6ac169734d5e3`.

Addresses below are decoded 8051 CODE offsets, not HEX text line numbers.
XDATA addresses name registers or internal RAM. No proprietary MCU source
has been copied into the driver.

## C: keep ambiguous interpretations visible

The dump's reader at 26F4 transfers R6/R7 to DPTR, reads XDATA and returns
the byte in R7. The caller at 25AD reads B8B0, masks 01, and returns one
when the bit is set. The host exposes `clock_detect_bit0`, together with
`rx_status_raw`, as telemetry only.

The caller at 24FD reads the same B8B0 but masks 40. Its tail is
`ef 54 40 70 03 7f 01 22 7f 00 22`: a set bit returns zero.
The recovered `HDMI_PLL_locked()` function instead returns true for a set
bit. That conflict prevents assigning a trustworthy lock polarity.
The host exposes `pll_status_bit6`, never a guessed `pll_locked`.

The reversed lane routine writes C201 and C256, combining lane selection
with other configuration bits. It is a DSI panel firmware, whereas NanoKVM
uses CSI. The host reads both configuration bytes without deriving a lane
count, changing either register, or copying the initialization sequence.
An all-FF B8B0 is unavailable, not proof of clock or lock. The clock-detect/bit-6
values are reread around the timing sample. A status change invalidates only
the optional diagnostic result.

## D: published telemetry layout 25.05.13.01

CODE 755E..757F copies four constant bytes from CODE 1003..1006 into
XDATA E080..E083. Those constants are `25 05 13 01`, supporting our raw
firmware representation. E080 is not a measured 37 MHz pixel clock.

CODE 33ED..35FF publishes telemetry when the event changes. It writes
E084 **before** filling the payload; zero event clears the fields. E084
is neither live HPD nor an atomic snapshot marker. The producer also
handles audio events separately.

The debug strings at 5E87 and 5EC7 identify the measured block as **MIPI TX**.
The callers at 43BF..442A associate the producer's internal words with
sync widths, front porches and back porches. Following these words through
the publisher establishes:

| Register | Producer source | Interpretation |
| --- | --- | --- |
| E085..E087 | internal 0563..0566 via 750B | 24 raw clock bits; units unqualified |
| E088/89, E08C/8D | 058C, 058E | H total, H active |
| E098/99, E09A/9B, E09C/9D | 0592, 0590, 0594 | H sync, front, back |
| E08A/8B, E08E/8F | 0596, 0598 | V total, V active |
| E09E/9F, E0A1/A2, E0A3/A4 | 059C, 059A, 059E | V sync, front, back |
| E0A5, E0A6 | 05A0, 05A1 | raw H/V polarity |
| E092..E094 | internal 055F..0562 through division helper | existing raw byte clock |

Words are published big-endian. E0A0 is skipped by the producer and reader.
At 3452, format 0A selects direct horizontal-word copies through helper
74F8. Other formats use helper 74FF, which shifts the 16-bit word right by
one before publication. Diagnostic horizontal scaling is therefore one
for format 0A and two for other formats, for this firmware version only.
The existing capture-size path is not changed by this diagnostic work.

The host gates extra reads on all four version bytes, validates timing
sums, rejects FFFF words and rereads metadata/timings. Raw clock jitter
does not invalidate a snapshot. A different firmware version retains the
baseline map; no clock/FPS/date, independent HDMI geometry, or HDCP state
is inferred. A matching version is a layout hint, not an image hash.

## Verification and remaining limits

`python3 tests/test-hdmi-sessions.py` exercises the actual decoder, reader
and production poll/control functions. It covers every diagnostic I/O and
cleanup failure, late optional faults, both horizontal branches, unknown
versions, raw C status, invalid sums/FFFF and changes during sampling.
D diagnostics are asserted to issue no writes or internal-ID reads.
C adds no writes beyond its existing access and timing-measurement session.

Physical C/D validation remains necessary for NanoKVM firmware variants.
The new fields cannot affect capture geometry, stream state or signal
publication. Reading diagnostics never programs flash, trains PLLs, changes
lanes or toggles the D stream.
