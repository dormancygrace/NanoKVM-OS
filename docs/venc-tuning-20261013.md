# Video encoder tuning (2026-10-12/13)

Measured on the stand: SG2002, kernel r1, 2560x1440@60 from a Windows PC with an RTX 5060 Ti, Direct and WebRTC in Chrome. The tools were a uprobe on `VideoSource.publish` for frame sizes and types, `/proc/cvitek/venc` for HW encode time and frame counts, `/proc/<pid>/stat` for server CPU and ION debugfs for video memory.

## What was wrong

- **The rate-control limits were never applied.**
  - The encoder is opened by `CVI_VENC_StartRecvFrame` and reads the QP limits, the I-frame proportion and the start QP only then (`cviInitEncoder`).
  - `kvm_mmf` called `SetRcParam` after it. That only updated the stored configuration, so `/proc` showed the intended values while the stream ran on the driver defaults: QP down to 1 and unbounded I frames.
- **SmartP H.265 with GOP 30 at 60 fps produced**:
  - a virtual I frame every 0.5 s;
  - a background IDR every 2.5 s, about 250 KB each at 1440p;
  - an extra long-term reference frame in ION.
- **No viewer needed periodic IDRs.**
  - A new subscriber, WebRTC PLI, Direct recovery and RustDesk all ask for one.
  - The GOP setting and the GOP mode only cost bits, delay and memory.

## Changes

- `kvm_mmf`:
  - RC parameters are set before `StartRecvFrame`.
  - The encoder runs NormalP only, with IDRs on request plus a safety IDR every 10 s; a requested IDR restarts the count.
  - QP floor 20 for P and I frames, chosen visually against 16/14 and 14/12.
  - H.264 uses the High profile (8x8 transform; Main already used CABAC).
  - The stream buffer is one byte per pixel (3.5 MiB at 1440p, 8 MiB at 2160p) instead of 1 MiB. At 20 Mbit/s, 1440p IDRs reached 650 KB plus queued deltas.
  - A frame that does not complete within 3 s resets the encoder channel and releases its input lease. Before, both VPSS buffers stayed held and capture stopped for good.
- **Tuning override** for measurements: `/run/nanokvm-venc-tune` (volatile, `key=value`), read when a channel is created. Keys:
  - `idr_seconds`, `h264_high`;
  - `min_qp`, `min_iqp`, `max_qp`, `max_iqp`, `max_iprop`;
  - `ip_qp_delta`, `row_qp_delta`, `thrd_lv`, `buf_kb`;
  - `stats=1`, which writes per-second statistics (bytes, frame types, mean QP) to `/run/nanokvm-venc-stats`.
- Server:
  - the GOP mode is always NormalP, and a saved or requested SmartP reads as NormalP;
  - the native libraries' stderr reaches the application log, rate limited (it was discarded);
  - the measured stream bitrate is in `/run/nanokvm/now_kbps` and in the screen status.
- Web:
  - video modes (the monitor and the stream rate as one choice) instead of presets;
  - the picture settings shared by every viewer are separated from this browser's transport and player;
  - automatic or economy bitrate;
  - UHD offers the memory switch and reboot;
  - the toolbar menu has mode, bitrate and transport rows and shows the measured size, rate and bitrate.

## Measurements

H.265, 2560x1440@60, CBR 12 Mbit/s. Each configuration was run twice, alternating, 30 s per run.

Static desktop with a running stopwatch:

| | SmartP, driver default QP | NormalP, QP floor 20 |
|---|---|---|
| bitrate | 5.46 Mbit/s | 0.51 Mbit/s |
| delta frame p95 | 44 KB | 1.2 KB |
| largest frame | 230 KB | 85 KB |
| IDRs in 30 s | 12 | 3 |
| server CPU | 14.0 % | 10.3 % |
| ION | 44.3 MiB | 38.5 MiB |
| HW encode time | 14.6 ms | 14.6 ms |

QP floor against bitrate on the same scene:

| P/I floor | 20/20 | 18/16 | 16/14 | 14/12 | 2/0 |
|---|---|---|---|---|---|
| bitrate (Mbit/s) | 0.51 | 0.57 | 0.66 | 0.77 | 9.7 |

At 2/0, CBR spends the budget re-refining a static picture every frame (mean QP 12.5).

Motion (full-screen video): both configurations sit at the CBR target. NormalP has 3 IDRs per 30 s of 130–250 KB instead of 12 of 340–460 KB, and ION is 5.8 MiB lower.

Bitrate against HW time on motion, 60 s per step:

| | 12 Mbit/s | 20 Mbit/s |
|---|---|---|
| actual bitrate | 10.3–11.4 Mbit/s | 18.9 Mbit/s |
| seconds below 60 encoded frames | 0 | 0 |
| average HW time | 15.0–15.3 ms | 15.2 ms |
| worst-second HW time | 15.9–16.5 ms | 16.0 ms |

The HW time is set by the resolution and rate, not the bitrate. The limit for the bitrate is server CPU and the network: about 19 % CPU at 12 Mbit/s and 26 % at 20 Mbit/s.

H.264 High against Main: same HW time (15.3 against 15.4–15.6 ms). Chrome plays High in Direct and in WebRTC, although the SDP still advertises `4d402a`. The QP difference was below the scene variance of the test video.

## Not done, and why

- **Super-frame strategy (re-encode an oversized frame).** It encodes the frame twice, and there is no room for that at 1440p60.
- **SAO and the H.265 fast preset** would need a driver module parameter. There is no HW-time headroom at 1440p60 (about 15 of 16.7 ms); this is worth trying only at lower pixel rates.
- **Intra refresh** is hard-coded off in the driver. Browsers still need an IDR to start decoding, so it would only help loss recovery.
- **VBR / AVBR.**
  - VBR does not lower the static bitrate; the QP floor does.
  - AVBR depends on a motion level from VI that frames sent with `SendFrame` do not carry.
- **Native VUI** (`CVI_VENC_SetH26xVui`) could replace the server-side SPS rewrite. Not done here.

## Test notes

- The debug kernel (r3dbg) is not representative. At 20 Mbit/s on heavy motion it stuttered, and the stand once hung (watchdog reset, no panic logged). The same stream on r1 is smooth.
- A capture stall seen once with H.264 High at 20 Mbit/s over WebRTC had this signature: one VPSS buffer held by the user lease, the encoder holding an unfetched frame, and a 179 ms frame just before. The bounded wait and channel reset above turn such a stall into a logged recovery.
