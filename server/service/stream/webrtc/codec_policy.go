package webrtc

import "NanoKVM-Server/service/stream"

const h265WebRTCError = "h265-webrtc-disabled"

// H.265 WebRTC can hang the device with CryptoDMA even below QHD.
// Keep the block independent of geometry, signal presence and AES overrides.
// H.265 Direct uses a separate transport and remains available.
func h265WebRTCBlocked(codec stream.VideoCodec) bool {
	return codec == stream.VideoCodecH265
}
