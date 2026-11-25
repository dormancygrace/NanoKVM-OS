#pragma once

// Outbound SRTP for AES128_CM_HMAC_SHA1_80 (the profile the server prefers, as the Go server's srtpaccel) with the
// SG2002's CryptoDMA engine: the RTP packets of a video frame go to the engine in one request, which runs AES-CTR of
// each payload and SHA-1 of the inner HMAC hash as one chain per packet (HWCRYPTO_CTR_SHA1); the CPU does the
// payload's last partial block and the HMAC's outer block. A packet the engine cannot take, or every packet when the
// engine is missing or failed, is protected in software. The rollover counters are kept per SSRC here (the protector
// owns the connection's outbound RTP, retransmissions included).

#include <cstddef>
#include <cstdint>
#include <memory>
#include <vector>

namespace nkos::video::srtp_frame {

// Installs the protector factory for every WebRTC connection (libdatachannel patch 0001). Once, at start.
void install();

// The protector itself, for the differential check: engine false protects in software only.
class Protector {
public:
	static std::unique_ptr<Protector> make(const unsigned char *master_key, const unsigned char *master_salt, bool engine);
	virtual ~Protector() = default;
	// Packets with at least 128 bytes of room after each; sizes become the protected sizes. False on a malformed
	// packet.
	virtual bool protect(std::byte **packets, std::size_t *sizes, std::size_t count) = 0;
};

} // namespace nkos::video::srtp_frame
