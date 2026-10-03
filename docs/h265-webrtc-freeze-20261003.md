# H.265 WebRTC first-GOP freeze

The report was reproduced in Chrome Main on app 2.0_beta8-r17 / Linux
7.2.6-nanokvm-os-r1, with H.265 SmartP, 1920x1080, 60 FPS and 10 Mbps.
The encoder continued at about 59 FPS and RTP packets kept arriving with no
reported packet loss. Chrome stopped at 30 decoded frames and one decoded
keyframe. Received frames increased from 556 to 1,634 while decoded frames
remained at 30. A later reproduction also ended in device network loss; the
network outage is not established as a consequence of this decoder failure.

## Packetization defect and change

[Chromium H26xPacketBuffer](https://chromium.googlesource.com/external/webrtc/+/refs/heads/master/modules/video_coding/h26x_packet_buffer.cc)
requires VPS, SPS and PPS within every H.265 IRAP access unit, including CRA.
SmartP may produce CRA without repeated parameter sets. Pion RTP 1.10.5 consumes
its parameter-set cache after emitting the next NAL. Our previous packetizer
therefore could not provide the cached decoder configuration at a later CRA.

The new per-peer H.265 payloader retains independently owned VPS/SPS/PPS from
the native single-configuration stream. When an IRAP lacks any of these, it
supplies the latest complete chain before the original video NALs. Partial
in-band updates replace retained parameters without duplicated parameter NALs.
Normal delta frames and already-complete keyframes keep their existing wire
behavior. Both packetizer entry points use the wrapper; adaptive PMTU changes
preserve payloader state, RTP sequence and timestamp origin.

This fixes a demonstrated packetization defect consistent with the measured
first-GOP freeze. Live packet capture and validation of the corrected server on
the device remain outstanding. No claim of verified device recovery is made.

## Validation and release scope

- Regression test failed before the fix: a later CRA carried only NAL type 21,
  while Chromium requires types 32, 33, 34 and 21 in that access unit.
- Host stream/WebRTC tests and race checks passed after the change.
- Normal RISC-V production server cross-build passed using Go 1.27.1, the
  updated native bundle and the updated Pion dependencies. No teststub or
  nanokvm_profile build tags are present. Build outputs and provenance are in
  work/h265-freeze-20261003; the source implementation commit is 0d75df7.
- Tests reconstruct RFC 7798 single NAL, AP and FU packets independently and
  check retained ownership, all IRAP types 16-23, configuration updates,
  fragmentation, marker, MTU budget and continuous RTP clock/sequence.
- Source-only change for the future v2.1-b1, following the user's instruction in
  the adjacent release task. No server, kernel, configuration or package was
  installed on the device by this investigation.
- Existing H.264 queue/batching/GSO/AES startup choices, native GOP mode and
  Pion dependency source trees remain unchanged.
