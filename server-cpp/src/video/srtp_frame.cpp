#include "video/srtp_frame.hpp"

#include "common/log.hpp"

#include <openssl/aes.h>
#include <openssl/sha.h>
#include <rtc/rtc.hpp>

#include <atomic>
#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <map>
#include <mutex>
#include <sys/ioctl.h>
#include <unistd.h>

// The OpenSSL low-level AES and SHA-1 calls take one block at a time with no context set-up: what a per-packet tail
// and outer HMAC block need (the EVP interface costs more than the block here).
#pragma GCC diagnostic ignored "-Wdeprecated-declarations"

namespace nkos::video::srtp_frame {

namespace {

// The driver's ABI (platform/modules/hwcrypto/hwcrypto.h).
constexpr unsigned abi = 1;
constexpr std::size_t engine_max = 4096;
constexpr std::size_t chain_max = 64;
struct ChainPacket {
	std::uint64_t data;
	std::uint32_t length, crypt_offset, crypt_length, reserved;
	std::uint8_t iv[16], state[20];
	std::uint32_t reserved2;
};
struct ChainRequest {
	std::uint32_t version, count;
	std::uint8_t key[16], state[20];
	std::uint32_t reserved;
	std::uint64_t packets;
	std::uint32_t reserved2[4];
};
static_assert(sizeof(ChainPacket) == 64);
static_assert(sizeof(ChainRequest) == 72);
constexpr unsigned long chain_ioctl = (3ul << 30) | (sizeof(ChainRequest) << 16) | ('K' << 8) | 0xc5;

constexpr std::size_t tag_length = 10;

// The engine, shared by every connection; given up (software from then on) after the first failure.
std::mutex device_mutex;
int device = -2;  // -2 not opened yet, -1 none
std::atomic<bool> device_failed{false};

int engine()
{
	std::lock_guard lock(device_mutex);
	if (device == -2) {
		device = ::open("/dev/nanokvm-hwcrypto", O_RDWR | O_CLOEXEC);
		if (device < 0) log::info("SRTP AES-CTR: device unavailable ({}); using software", std::strerror(errno));
		else log::info("SRTP AES-CTR: SG2002 packet accelerator enabled");
	}
	return device_failed ? -1 : device;
}

void put32(std::byte *p, std::uint32_t v)
{
	p[0] = std::byte(v >> 24);
	p[1] = std::byte(v >> 16);
	p[2] = std::byte(v >> 8);
	p[3] = std::byte(v);
}

// RFC 3711 4.3: the AES-CM key derivation, key_derivation_rate 0.
void derive(const AES_KEY &master, const unsigned char *salt, unsigned char label, unsigned char *out, std::size_t n)
{
	unsigned char iv[16] = {};
	std::memcpy(iv, salt, 14);
	iv[7] ^= label;
	for (std::size_t done = 0, counter = 0; done < n; counter++) {
		iv[14] = static_cast<unsigned char>(counter >> 8);
		iv[15] = static_cast<unsigned char>(counter);
		unsigned char block[16];
		AES_encrypt(iv, block, &master);
		std::size_t take = std::min<std::size_t>(16, n - done);
		std::memcpy(out + done, block, take);
		done += take;
	}
}

struct Roc {
	std::uint32_t roc = 0;
	std::uint16_t highest = 0;
	bool seen = false;
};

class Impl final : public Protector {
public:
	Impl(const unsigned char *master_key, const unsigned char *master_salt, bool use_engine) : use_engine_(use_engine)
	{
		AES_KEY master;
		AES_set_encrypt_key(master_key, 128, &master);
		derive(master, master_salt, 0, key_, 16);
		unsigned char auth[20];
		derive(master, master_salt, 1, auth, 20);
		derive(master, master_salt, 2, salt_, 14);
		AES_set_encrypt_key(key_, 128, &aes_);
		unsigned char ipad[64] = {}, opad[64] = {};
		std::memcpy(ipad, auth, 20);
		std::memcpy(opad, auth, 20);
		for (int i = 0; i < 64; i++) {
			ipad[i] ^= 0x36;
			opad[i] ^= 0x5c;
		}
		SHA1_Init(&inner_);
		SHA1_Update(&inner_, ipad, 64);
		SHA1_Init(&outer_);
		SHA1_Update(&outer_, opad, 64);
		const unsigned h[5] = {inner_.h0, inner_.h1, inner_.h2, inner_.h3, inner_.h4};
		for (int i = 0; i < 5; i++) put32(reinterpret_cast<std::byte *>(inner_state_ + 4 * i), h[i]);
	}

	bool protect(std::byte **packets, std::size_t *sizes, std::size_t count) override
	{
		std::vector<Item> items(count);
		for (std::size_t i = 0; i < count; i++)
			if (!prepare(packets[i], sizes[i], items[i])) return false;
		std::size_t done = 0;
		int fd = use_engine_ ? engine() : -1;
		if (fd >= 0) {
			// The engine takes whole 16-byte blocks of payloads at a 4-byte aligned offset, at most engine_max
			// bytes with the hash padding, chain_max packets a request; the others are done in software.
			while (done < count) {
				std::size_t first = done, n = 0;
				ChainPacket chain[chain_max];
				while (done + n < count && n < chain_max) {
					Item &it = items[done + n];
					std::size_t crypt = (it.size - it.header) & ~std::size_t(15);
					std::size_t end = it.size + 4;
					std::size_t total = (end + 9 + 63) & ~std::size_t(63);
					if (crypt == 0 || it.header % 4 != 0 || total > engine_max || total - it.size > rtc::SrtpProtectorRoom) break;
					tail(it);
					put32(it.data + it.size, it.roc);
					it.data[end] = std::byte(0x80);
					std::memset(it.data + end + 1, 0, total - 8 - end - 1);
					std::uint64_t bits = std::uint64_t(64 + end) * 8;
					for (int b = 0; b < 8; b++) it.data[total - 1 - b] = std::byte(bits >> (8 * b));
					ChainPacket &p = chain[n];
					std::memset(&p, 0, sizeof p);
					p.data = reinterpret_cast<std::uintptr_t>(it.data);
					p.length = static_cast<std::uint32_t>(total);
					p.crypt_offset = static_cast<std::uint32_t>(it.header);
					p.crypt_length = static_cast<std::uint32_t>(crypt);
					std::memcpy(p.iv, it.iv, 16);
					n++;
				}
				if (n == 0) break;
				ChainRequest r{};
				r.version = abi;
				r.count = static_cast<std::uint32_t>(n);
				std::memcpy(r.key, key_, 16);
				std::memcpy(r.state, inner_state_, 20);
				r.packets = reinterpret_cast<std::uintptr_t>(chain);
				std::size_t completed = 0;
				if (::ioctl(fd, chain_ioctl, &r) == 0) {
					completed = r.count;
				} else if (!device_failed.exchange(true)) {
					log::warn("SRTP AES-CTR: hardware disabled ({}); using software", std::strerror(errno));
				}
				for (std::size_t k = 0; k < completed; k++) {
					Item &it = items[first + k];
					unsigned char digest[20];
					for (int b = 0; b < 20; b++) digest[b] = chain[k].state[b];
					finish(it, digest);
				}
				// A packet the engine did not take goes back to plaintext: the partial block's key stream again.
				for (std::size_t k = completed; k < n; k++) tail(items[first + k]);
				done = first + completed;
				if (completed < n) break;
			}
		}
		for (std::size_t i = done; i < count; i++) software(items[i]);
		for (std::size_t i = 0; i < count; i++) sizes[i] = items[i].size + tag_length;
		return true;
	}

private:
	struct Item {
		std::byte *data;
		std::size_t size, header;
		std::uint32_t roc;
		unsigned char iv[16];
	};

	// Header length, rollover counter and IV of an RTP packet.
	bool prepare(std::byte *data, std::size_t size, Item &it)
	{
		if (size < 12) return false;
		auto u8 = [&](std::size_t i) { return std::to_integer<unsigned>(data[i]); };
		std::size_t header = 12 + 4 * (u8(0) & 0x0f);
		if (u8(0) & 0x10) {
			if (size < header + 4) return false;
			header += 4 + 4 * ((u8(header + 2) << 8) | u8(header + 3));
		}
		if (size < header) return false;
		std::uint16_t seq = static_cast<std::uint16_t>((u8(2) << 8) | u8(3));
		std::uint32_t ssrc = (u8(8) << 24) | (u8(9) << 16) | (u8(10) << 8) | u8(11);
		it.data = data;
		it.size = size;
		it.header = header;
		it.roc = rollover(ssrc, seq);
		// IV = (k_s * 2^16) XOR (SSRC * 2^64) XOR (index * 2^16), index = ROC * 2^16 + SEQ.
		std::memset(it.iv, 0, 16);
		std::memcpy(it.iv, salt_, 14);
		for (int b = 0; b < 4; b++) it.iv[4 + b] ^= static_cast<unsigned char>(ssrc >> (24 - 8 * b));
		std::uint64_t index = (std::uint64_t(it.roc) << 16) | seq;
		for (int b = 0; b < 6; b++) it.iv[8 + b] ^= static_cast<unsigned char>(index >> (40 - 8 * b));
		return true;
	}

	// The sender's rollover counter for seq (RFC 3711 3.3.1): the highest sequence number sent so far decides
	// whether seq wrapped forward or is an older (retransmitted) one.
	std::uint32_t rollover(std::uint32_t ssrc, std::uint16_t seq)
	{
		Roc &r = rocs_[ssrc];
		if (!r.seen) {
			r = Roc{0, seq, true};
			return 0;
		}
		std::uint32_t v = r.roc;
		if (r.highest < 32768) {
			if (seq > r.highest && seq - r.highest > 32768) v = r.roc - 1;
		} else if (seq < r.highest - 32768) {
			v = r.roc + 1;
		}
		if (v == r.roc + 1 || (v == r.roc && seq > r.highest)) {
			r.roc = v;
			r.highest = seq;
		}
		return v;
	}

	// XORs the payload's last partial block with its key stream (the engine takes whole blocks).
	void tail(Item &it)
	{
		std::size_t payload = it.size - it.header;
		std::size_t blocks = payload / 16;
		std::size_t rest = payload % 16;
		if (!rest) return;
		unsigned char counter[16], stream[16];
		std::memcpy(counter, it.iv, 16);
		std::uint64_t low = 0;
		for (int b = 0; b < 8; b++) low = (low << 8) | counter[8 + b];
		std::uint64_t added = low + blocks;
		if (added < low)
			for (int b = 7; b >= 0 && ++counter[b] == 0; b--) {
			}
		for (int b = 0; b < 8; b++) counter[8 + b] = static_cast<unsigned char>(added >> (56 - 8 * b));
		AES_encrypt(counter, stream, &aes_);
		std::byte *p = it.data + it.header + 16 * blocks;
		for (std::size_t i = 0; i < rest; i++) p[i] ^= std::byte(stream[i]);
	}

	// The HMAC's outer block over the inner digest; the 80-bit tag after the payload.
	void finish(Item &it, const unsigned char *inner)
	{
		SHA_CTX ctx = outer_;
		SHA1_Update(&ctx, inner, 20);
		unsigned char tag[20];
		SHA1_Final(tag, &ctx);
		std::memcpy(it.data + it.size, tag, tag_length);
	}

	void software(Item &it)
	{
		// AES-CTR over the payload.
		unsigned char counter[16], stream[16];
		std::memcpy(counter, it.iv, 16);
		std::byte *p = it.data + it.header;
		std::size_t payload = it.size - it.header;
		for (std::size_t off = 0; off < payload; off += 16) {
			AES_encrypt(counter, stream, &aes_);
			std::size_t take = std::min<std::size_t>(16, payload - off);
			for (std::size_t i = 0; i < take; i++) p[off + i] ^= std::byte(stream[i]);
			for (int b = 15; b >= 0 && ++counter[b] == 0; b--) {
			}
		}
		// HMAC-SHA1 over header, payload and ROC.
		SHA_CTX ctx = inner_;
		SHA1_Update(&ctx, it.data, it.size);
		unsigned char roc[4];
		put32(reinterpret_cast<std::byte *>(roc), it.roc);
		SHA1_Update(&ctx, roc, 4);
		unsigned char digest[20];
		SHA1_Final(digest, &ctx);
		finish(it, digest);
	}

	bool use_engine_;
	unsigned char key_[16], salt_[14], inner_state_[20];
	AES_KEY aes_;
	SHA_CTX inner_, outer_;
	std::map<std::uint32_t, Roc> rocs_;
};

class Adapter final : public rtc::SrtpProtector {
public:
	explicit Adapter(std::unique_ptr<Protector> p) : p_(std::move(p)) {}
	bool protect(std::byte **packets, std::size_t *sizes, std::size_t count) override { return p_->protect(packets, sizes, count); }

private:
	std::unique_ptr<Protector> p_;
};

} // namespace

std::unique_ptr<Protector> Protector::make(const unsigned char *master_key, const unsigned char *master_salt, bool use_engine)
{
	return std::make_unique<Impl>(master_key, master_salt, use_engine);
}

void install()
{
	rtc::SetSrtpProtectorFactory([](const std::string &profile, const unsigned char *key, std::size_t key_len,
	                                const unsigned char *salt, std::size_t salt_len) -> std::unique_ptr<rtc::SrtpProtector> {
		if (profile != "SRTP_AES128_CM_SHA1_80" || key_len != 16 || salt_len != 14) return nullptr;
		return std::make_unique<Adapter>(Protector::make(key, salt, true));
	});
}

} // namespace nkos::video::srtp_frame
