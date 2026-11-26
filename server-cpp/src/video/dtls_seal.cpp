#include "video/dtls_seal.hpp"

#include "common/log.hpp"

#include <openssl/aes.h>
#include <openssl/evp.h>
#include <openssl/rand.h>
#include <rtc/rtc.hpp>

#include <atomic>
#include <cerrno>
#include <cstdint>
#include <cstring>
#include <fcntl.h>
#include <mutex>
#include <sys/ioctl.h>
#include <unistd.h>

// AES_encrypt of one block for the hash key: no context set-up.
#pragma GCC diagnostic ignored "-Wdeprecated-declarations"

namespace nkos::video::dtls_seal {

namespace {

// The driver's ABI (platform/modules/hwcrypto/hwcrypto.h).
constexpr unsigned abi = 1;
constexpr std::size_t batch_max = 16, batch_bytes = 65536, record_max = 4096;
struct BatchRecord {
	std::uint8_t j0[16], aad[16];
	std::uint32_t aad_length, length;
	std::uint64_t data;
};
struct BatchRequest {
	std::uint32_t version, count, key_length, reserved;
	std::uint8_t key[32], h[16];
	std::uint64_t records;
	std::uint32_t reserved2[4];
};
static_assert(sizeof(BatchRecord) == 48);
static_assert(sizeof(BatchRequest) == 88);
constexpr unsigned long batch_ioctl = (3ul << 30) | (sizeof(BatchRequest) << 16) | ('K' << 8) | 0xc6;

// What one record takes of the driver's buffer.
std::size_t footprint(std::size_t length) { return ((length + 15) & ~std::size_t{15}) + 16; }

bool software(const std::uint8_t *key, rtc::DtlsSealRecord &r)
{
	EVP_CIPHER_CTX *ctx = EVP_CIPHER_CTX_new();
	int n = 0;
	bool ok = ctx && EVP_EncryptInit_ex(ctx, EVP_aes_128_gcm(), nullptr, key, r.nonce) == 1 &&
	          EVP_EncryptUpdate(ctx, nullptr, &n, r.aad, sizeof(r.aad)) == 1 &&
	          EVP_EncryptUpdate(ctx, r.data, &n, r.data, static_cast<int>(r.length)) == 1 &&
	          EVP_EncryptFinal_ex(ctx, r.data + n, &n) == 1 &&
	          EVP_CIPHER_CTX_ctrl(ctx, EVP_CTRL_GCM_GET_TAG, 16, r.data + r.length) == 1;
	EVP_CIPHER_CTX_free(ctx);
	return ok;
}

class Sealer final : public rtc::DtlsRecordSealer {
public:
	std::size_t seal(const std::uint8_t *key, rtc::DtlsSealRecord *records, std::size_t count) override
	{
		int fd = engine();
		if (fd < 0) return 0;
		std::uint8_t h[16] = {};
		AES_KEY aes;
		AES_set_encrypt_key(key, 128, &aes);
		AES_encrypt(h, h, &aes);
		// A request writes all of its records or none (EFAULT aside, which these buffers cannot raise): the records
		// sealed are always a prefix.
		std::size_t first = 0;
		while (first < count) {
			std::size_t n = 0, bytes = 0;
			while (first + n < count && n < batch_max && records[first + n].length > 0 &&
			       records[first + n].length <= record_max && bytes + footprint(records[first + n].length) <= batch_bytes)
				bytes += footprint(records[first + n++].length);
			if (n == 0) break;
			if (!request(fd, key, h, records + first, n)) {
				if (!failed_.exchange(true))
					log::warn("DTLS records: the engine failed ({}); sealed in software", std::strerror(errno));
				break;
			}
			first += n;
		}
		return first;
	}

private:
	std::mutex mutex_;
	int device_ = -2; // -2 not opened yet, -1 none
	std::atomic<bool> failed_{false};

	int engine()
	{
		if (failed_) return -1;
		std::lock_guard lock(mutex_);
		if (device_ == -2) {
			device_ = ::open("/dev/nanokvm-hwcrypto", O_RDWR | O_CLOEXEC);
			if (device_ < 0) {
				log::info("DTLS records: device unavailable ({}); sealed in software", std::strerror(errno));
			} else if (!self_check(device_)) {
				log::warn("DTLS records: the engine's seal differs from software; sealed in software");
				failed_ = true;
			} else {
				log::info("DTLS records: AES-GCM bursts sealed on the SG2002 engine");
			}
		}
		return failed_ ? -1 : device_;
	}

	static bool request(int fd, const std::uint8_t *key, const std::uint8_t *h, rtc::DtlsSealRecord *records,
	                    std::size_t n)
	{
		BatchRecord items[batch_max] = {};
		for (std::size_t i = 0; i < n; i++) {
			std::memcpy(items[i].j0, records[i].nonce, 12);
			items[i].j0[15] = 1;
			std::memcpy(items[i].aad, records[i].aad, sizeof(records[i].aad));
			items[i].aad_length = sizeof(records[i].aad);
			items[i].length = static_cast<std::uint32_t>(records[i].length);
			items[i].data = reinterpret_cast<std::uintptr_t>(records[i].data);
		}
		BatchRequest req{};
		req.version = abi;
		req.count = static_cast<std::uint32_t>(n);
		req.key_length = 16;
		std::memcpy(req.key, key, 16);
		std::memcpy(req.h, h, 16);
		req.records = reinterpret_cast<std::uintptr_t>(items);
		int r = ::ioctl(fd, batch_ioctl, &req);
		OPENSSL_cleanse(req.key, sizeof(req.key));
		return r == 0;
	}

	// Two records of different lengths through the engine and through EVP: the same bytes, or the engine is not used.
	bool self_check(int fd)
	{
		std::uint8_t key[16], a[1205 + 16], b[1205 + 16], c[33 + 16], e[33 + 16];
		rtc::DtlsSealRecord r[2], s[2];
		if (RAND_bytes(key, sizeof key) != 1 || RAND_bytes(a, 1205) != 1 || RAND_bytes(c, 33) != 1) return false;
		std::memcpy(b, a, 1205);
		std::memcpy(e, c, 33);
		for (int i = 0; i < 2; i++) {
			if (RAND_bytes(r[i].nonce, 12) != 1 || RAND_bytes(r[i].aad, 13) != 1) return false;
			s[i] = r[i];
		}
		r[0].data = a, r[0].length = 1205, s[0].data = b, s[0].length = 1205;
		r[1].data = c, r[1].length = 33, s[1].data = e, s[1].length = 33;
		std::uint8_t h[16] = {};
		AES_KEY aes;
		AES_set_encrypt_key(key, 128, &aes);
		AES_encrypt(h, h, &aes);
		if (!request(fd, key, h, r, 2) || !software(key, s[0]) || !software(key, s[1])) return false;
		return std::memcmp(a, b, sizeof a) == 0 && std::memcmp(c, e, sizeof c) == 0;
	}
};

} // namespace

void install() { rtc::SetDtlsRecordSealer(std::make_shared<Sealer>()); }

} // namespace nkos::video::dtls_seal
