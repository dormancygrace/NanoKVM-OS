package webrtc

import "github.com/pion/rtp"

// packetizeCapturedVideo preserves time spent between captured frames, including
// frames discarded by the bounded subscriber queue. Advancing the RTP clock by
// one nominal frame duration per delivered frame compresses time under load and
// makes the receiver's jitter estimate grow. Timestamp is the existing monotonic
// capture-session time in microseconds, not the time a blocked writer resumes.
func packetizeCapturedVideo(packetizer rtp.Packetizer, data []byte, timestamp int64) []*rtp.Packet {
	// Keep the packetizer's random initial timestamp as a fixed origin. Convert
	// absolute capture time once, avoiding per-frame truncation (e.g. 1499 ticks
	// for a nominal 60 Hz duration) and preserving natural uint32 RTP wraparound.
	packets := packetizer.Packetize(data, 0)
	offset := uint32(uint64(timestamp) * 90 / 1000)
	for _, packet := range packets {
		packet.Timestamp += offset
	}
	return packets
}
