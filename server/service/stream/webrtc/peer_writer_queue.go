package webrtc

import "NanoKVM-Server/service/stream"

const (
	// Thirty-two frames absorb measured Wi-Fi sender stalls while the byte
	// budget below bounds compressed storage independently of frame count.
	peerVideoQueueCapacity = 32
	peerVideoQueueBytes    = 2 * 1024 * 1024
	// video_pack_storage caps native payloads at 64 MiB and the capture path
	// adds nine bytes of packetizer headroom. Keep that as the one-frame bound.
	peerVideoSingleFrameBytes = (64 << 20) + 9
)

// Storage is the owned native frame allocation and includes the nine-byte
// packetizer headroom. Tests and synthetic frames may only provide Data.
func peerWriterFrameBytes(frame stream.VideoFrame) int {
	if len(frame.Storage) != 0 {
		return len(frame.Storage)
	}
	return len(frame.Data)
}
