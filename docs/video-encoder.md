# H.264 and H.265 video

H.265 Direct is the fresh-browser default. **Settings → Video** contains transport and codec selection; the quick menu retains resolution, FPS, quality and browser scale. GOP is under **Advanced and recovery**. See [Video settings](VIDEO.md) for HDMI input versus stream scaling and capability fallbacks.

Only CBR is exposed. Bitrate and GOP are device-wide settings shared by viewers. **QHD H.265 WebRTC is unstable and can freeze or restart the device; use Direct for QHD.**

## Capability handling

H.265 is selectable only when the browser reports support for the active
transport:

- Direct checks `VideoDecoder.isConfigSupported()` for the hardware stream's
  actual H.265 Main/Level 5 declaration, `hev1.1.6.L150.B0`, and requires
  WebCodecs in a secure context. H.264 is declared as the encoder's actual
  Main/constraint-set-1/Level 4.2 profile, `avc1.4D402A`;
- WebRTC checks `RTCRtpReceiver.getCapabilities("video")` for `video/H265`.

If a browser cannot decode H.265, the effective codec falls back to H.264. The
selector disables H.265 and records the fallback so the displayed selection
matches the stream.

## Stream API

New clients use one codec query parameter:

```text
/api/stream/video/direct?codec=h265
/api/stream/video?codec=h264
```

The codec is validated before the WebSocket upgrade. An explicit `rc=cbr` is
accepted for compatibility with development clients; other rate-control modes
are rejected. Existing `/api/stream/h264` and `/api/stream/h264/direct` clients
remain H.264/CBR compatibility endpoints.

The SG2002 has one hardware video encoder session. Direct and WebRTC viewers
using the same codec share it. Bitrate and GOP remain device-wide Screen state,
so changes made through the established menu are applied to the shared encoder
without creating per-browser profiles.

The native API returns one complete Annex-B access unit per call. It joins all
CVITEK VENC packs, recognizes H.264 IDR and H.265 IRAP NAL units, and marks the
result as a key or delta frame. Direct passes those access units to WebCodecs.
WebRTC uses Pion's H.264 or H.265 RTP payloader. The conservative fallback is a 1216-byte RTP packet including its base header. Reserving the largest negotiated SRTP tag (16 bytes), UDP (8) and IPv6 (40) gives 1280 bytes. Confirmed path discovery can change each viewer's budget, with smaller limits for known routes or relays; see [PMTU design](webrtc-pmtu-design.md). DTLS negotiates keys separately and does not add a DTLS header to each media packet.

The VENC pack array is allocated from the count returned by
`CVI_VENC_QueryStatus()` before `CVI_VENC_GetStream()` writes it. The public
access-unit interface still accepts at most eight packs and rejects larger
vendor results after releasing them. This prevents the previous fixed-size
vendor buffer from being overrun before its returned count could be checked.

## Building and testing

`libkvm.so` and `libkvm_mmf.so` must be rebuilt together with the Go server
because `mmf_venc_cfg_t` and the public video-read ABI changed. The release
builder refreshes MaixCDK components before compiling and links the server
against the newly staged libraries.

The transport/session tests do not require SG2002 hardware:

```sh
cd server
CGO_ENABLED=1 go test -tags teststub -race ./service/stream/... ./common
```

Use the matched RISC-V component build described in [BUILD.md](BUILD.md); the
frontend is checked with `pnpm lint` and `pnpm build` in `web/`.
