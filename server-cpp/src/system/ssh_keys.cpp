#include "system/ssh_keys.hpp"

#include "auth/session.hpp"
#include "auth/store.hpp"
#include "common/base64.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"

#include <glaze/glaze.hpp>
#include <openssl/bn.h>
#include <openssl/ec.h>
#include <openssl/evp.h>
#include <openssl/obj_mac.h>
#include <openssl/rand.h>

#include <cerrno>
#include <cstring>
#include <fcntl.h>
#include <memory>
#include <set>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::system::ssh_keys {

namespace {

constexpr size_t max_file = 256 * 1024;

struct BnFree {
	void operator()(BIGNUM *b) const { BN_free(b); }
};
using Bn = std::unique_ptr<BIGNUM, BnFree>;

// ---- the SSH wire format (golang.org/x/crypto/ssh messages.go) ----

bool parse_string(std::string_view &in, std::string_view &out)
{
	if (in.size() < 4) return false;
	std::uint32_t len = static_cast<std::uint32_t>(static_cast<std::uint8_t>(in[0])) << 24 |
	                    static_cast<std::uint32_t>(static_cast<std::uint8_t>(in[1])) << 16 |
	                    static_cast<std::uint32_t>(static_cast<std::uint8_t>(in[2])) << 8 | static_cast<std::uint8_t>(in[3]);
	in.remove_prefix(4);
	if (in.size() < len) return false;
	out = in.substr(0, len);
	in.remove_prefix(len);
	return true;
}

void write_string(std::string &out, std::string_view s)
{
	auto n = static_cast<std::uint32_t>(s.size());
	out += static_cast<char>(n >> 24);
	out += static_cast<char>(n >> 16);
	out += static_cast<char>(n >> 8);
	out += static_cast<char>(n);
	out += s;
}

// An mpint: sign and magnitude (big-endian, no leading zeros).
struct BigInt {
	bool negative = false;
	std::string magnitude;
	int bit_len() const
	{
		if (magnitude.empty()) return 0;
		int bits = static_cast<int>(magnitude.size()) * 8;
		for (auto top = static_cast<std::uint8_t>(magnitude[0]); !(top & 0x80); top = static_cast<std::uint8_t>(top << 1)) bits--;
		return bits;
	}
};

std::string strip(std::string s)
{
	size_t i = 0;
	while (i < s.size() && s[i] == 0) i++;
	return s.substr(i);
}

bool parse_int(std::string_view &in, BigInt &out)
{
	std::string_view c;
	if (!parse_string(in, c)) return false;
	if (!c.empty() && (static_cast<std::uint8_t>(c[0]) & 0x80)) {
		std::string inv(c);
		for (auto &b : inv) b = static_cast<char>(~b);
		Bn v(BN_bin2bn(reinterpret_cast<const unsigned char *>(inv.data()), static_cast<int>(inv.size()), nullptr));
		BN_add_word(v.get(), 1);
		std::string m(static_cast<size_t>(BN_num_bytes(v.get())), '\0');
		BN_bn2bin(v.get(), reinterpret_cast<unsigned char *>(m.data()));
		out.negative = true;
		out.magnitude = m;
	} else {
		out.negative = false;
		out.magnitude = strip(std::string(c));
	}
	return true;
}

void write_int(std::string &out, const BigInt &n)
{
	std::string c;
	if (n.magnitude.empty()) {
	} else if (!n.negative) {
		if (static_cast<std::uint8_t>(n.magnitude[0]) & 0x80) c += '\0';
		c += n.magnitude;
	} else {
		Bn v(BN_bin2bn(reinterpret_cast<const unsigned char *>(n.magnitude.data()), static_cast<int>(n.magnitude.size()), nullptr));
		BN_sub_word(v.get(), 1);
		std::string b(static_cast<size_t>(BN_num_bytes(v.get())), '\0');
		BN_bn2bin(v.get(), reinterpret_cast<unsigned char *>(b.data()));
		for (auto &x : b) x = static_cast<char>(~x);
		if (b.empty() || !(static_cast<std::uint8_t>(b[0]) & 0x80)) c += '\xff';
		c += b;
	}
	write_string(out, c);
}

// elliptic.Unmarshal: an uncompressed point on the curve.
bool valid_point(int nid, std::string_view data)
{
	std::unique_ptr<EC_GROUP, decltype(&EC_GROUP_free)> group(EC_GROUP_new_by_curve_name(nid), EC_GROUP_free);
	Bn p(BN_new());
	EC_GROUP_get_curve(group.get(), p.get(), nullptr, nullptr, nullptr);
	size_t size = static_cast<size_t>((BN_num_bits(p.get()) + 7) / 8);
	if (data.size() != 1 + 2 * size || data[0] != 4) return false;
	std::unique_ptr<EC_POINT, decltype(&EC_POINT_free)> pt(EC_POINT_new(group.get()), EC_POINT_free);
	return EC_POINT_oct2point(group.get(), pt.get(), reinterpret_cast<const unsigned char *>(data.data()), data.size(), nullptr) == 1 &&
	       !EC_POINT_is_at_infinity(group.get(), pt.get());
}

// ssh.ParsePublicKey for the key types the import accepts; nullopt for anything else (other types and certificates
// are refused by the import either way).
std::optional<PublicKey> parse_public_key(std::string_view in)
{
	std::string_view algo;
	if (!parse_string(in, algo)) return std::nullopt;
	PublicKey k;
	k.type = std::string(algo);
	write_string(k.wire, algo);
	if (algo == "ssh-rsa") {
		BigInt e, n;
		if (!parse_int(in, e) || !parse_int(in, n)) return std::nullopt;
		if (n.bit_len() > 16384 || e.bit_len() > 24) return std::nullopt;
		std::int64_t ev = 0;
		for (unsigned char c : e.magnitude) ev = ev << 8 | c;
		if (e.negative) ev = -ev;
		if (ev < 3 || !(ev & 1)) return std::nullopt;
		write_int(k.wire, e);
		write_int(k.wire, n);
		k.rsa_bits = n.bit_len();
		return in.empty() ? std::optional(k) : std::nullopt;
	}
	if (algo == "ecdsa-sha2-nistp256" || algo == "ecdsa-sha2-nistp384" || algo == "ecdsa-sha2-nistp521" ||
	    algo == "sk-ecdsa-sha2-nistp256@openssh.com") {
		bool sk = algo.starts_with("sk-");
		std::string_view curve, key, application;
		if (!parse_string(in, curve) || !parse_string(in, key)) return std::nullopt;
		if (sk && !parse_string(in, application)) return std::nullopt;
		int nid = 0;
		if (sk) {
			if (curve != "nistp256") return std::nullopt;
			nid = NID_X9_62_prime256v1;
		} else {
			if ("ecdsa-sha2-" + std::string(curve) != algo) return std::nullopt;
			nid = curve == "nistp256" ? NID_X9_62_prime256v1 : curve == "nistp384" ? NID_secp384r1 : NID_secp521r1;
		}
		if (!valid_point(nid, key)) return std::nullopt;
		write_string(k.wire, curve);
		write_string(k.wire, key);
		if (sk) write_string(k.wire, application);
		return in.empty() ? std::optional(k) : std::nullopt;
	}
	if (algo == "ssh-ed25519" || algo == "sk-ssh-ed25519@openssh.com") {
		std::string_view key, application;
		if (!parse_string(in, key)) return std::nullopt;
		bool sk = algo.starts_with("sk-");
		if (sk && !parse_string(in, application)) return std::nullopt;
		if (key.size() != 32) return std::nullopt;
		write_string(k.wire, key);
		if (sk) write_string(k.wire, application);
		return in.empty() ? std::optional(k) : std::nullopt;
	}
	return std::nullopt;
}

std::string_view trim_space_tab(std::string_view s)
{
	while (!s.empty() && (s.front() == ' ' || s.front() == '\t')) s.remove_prefix(1);
	while (!s.empty() && (s.back() == ' ' || s.back() == '\t')) s.remove_suffix(1);
	return s;
}

// parseAuthorizedKey: the key and comment after the type field.
std::optional<Parsed> parse_key_and_comment(std::string_view in)
{
	in = trim_space_tab(in);
	size_t i = in.find_first_of(" \t");
	if (i == std::string_view::npos) i = in.size();
	auto blob = base64::decode(in.substr(0, i));
	if (!blob) return std::nullopt;
	auto key = parse_public_key(*blob);
	if (!key) return std::nullopt;
	Parsed p;
	p.key = std::move(*key);
	p.comment = std::string(trim_space_tab(in.substr(i)));
	return p;
}

// ---- Unicode as Go's strings and unicode packages ----

std::optional<std::uint32_t> next_rune(std::string_view s, size_t &i)
{
	auto c = static_cast<std::uint8_t>(s[i]);
	if (c < 0x80) {
		i++;
		return c;
	}
	int n = c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 && c < 0xf0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
	if (!n || i + static_cast<size_t>(n) > s.size()) return std::nullopt;
	std::uint32_t cp = n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
	for (int k = 1; k < n; k++) {
		auto cc = static_cast<std::uint8_t>(s[i + static_cast<size_t>(k)]);
		if ((cc & 0xc0) != 0x80) return std::nullopt;
		cp = cp << 6 | (cc & 0x3f);
	}
	if ((n == 3 && (cp < 0x800 || (cp >= 0xd800 && cp <= 0xdfff))) || (n == 4 && (cp < 0x10000 || cp > 0x10ffff)))
		return std::nullopt;
	i += static_cast<size_t>(n);
	return cp;
}

bool valid_utf8(std::string_view s)
{
	for (size_t i = 0; i < s.size();)
		if (!next_rune(s, i)) return false;
	return true;
}

bool unicode_space(std::uint32_t r)
{
	switch (r) {
	case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xa0: case 0x1680: case 0x2028:
	case 0x2029: case 0x202f: case 0x205f: case 0x3000: return true;
	}
	return r >= 0x2000 && r <= 0x200a;
}

// strings.TrimSpace (valid UTF-8).
std::string_view trim_unicode(std::string_view s)
{
	size_t start = 0;
	while (start < s.size()) {
		size_t j = start;
		auto r = next_rune(s, j);
		if (!r || !unicode_space(*r)) break;
		start = j;
	}
	size_t end = s.size();
	while (end > start) {
		size_t k = end - 1;
		while (k > start && (static_cast<std::uint8_t>(s[k]) & 0xc0) == 0x80) k--;
		size_t j = k;
		auto r = next_rune(s, j);
		if (!r || j != end || !unicode_space(*r)) break;
		end = k;
	}
	return s.substr(start, end - start);
}

struct Entry {
	PublicKey key;
	std::string line;
};

// sshkeys.parse: every line on its own, so a malformed line refuses the whole input.
bool parse_input(std::string_view input, std::vector<Entry> &entries)
{
	static const std::set<std::string> allowed = {"ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384",
	                                              "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com",
	                                              "sk-ecdsa-sha2-nistp256@openssh.com"};
	if (input.size() > max_input || !valid_utf8(input)) return false;
	size_t pos = 0;
	for (;;) {
		size_t nl = input.find('\n', pos);
		std::string_view line = input.substr(pos, nl == std::string_view::npos ? std::string_view::npos : nl - pos);
		if (line.ends_with('\r')) line.remove_suffix(1);
		for (size_t i = 0; i < line.size();) {
			std::uint32_t r = *next_rune(line, i);
			if ((r < 0x20 || (r >= 0x7f && r < 0xa0)) && r != '\t') return false;
		}
		line = trim_unicode(line);
		if (!line.empty() && line[0] != '#') {
			auto p = parse_authorized_line(line);
			if (!p || !p->options.empty() || !allowed.count(p->key.type)) return false;
			if (p->key.type == "ssh-rsa" && p->key.rsa_bits < 2048) return false;
			std::string canonical = p->key.type + " " + base64::encode(p->key.wire);
			if (!p->comment.empty()) canonical += " " + p->comment;
			entries.push_back({p->key, canonical});
			if (entries.size() > 100) return false;
		}
		if (nl == std::string_view::npos) break;
		pos = nl + 1;
	}
	return !entries.empty();
}

std::string random_text()
{
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
	unsigned char b[26];
	RAND_bytes(b, sizeof b);
	std::string out;
	for (unsigned char c : b) out += alphabet[c % 32];
	return out;
}

std::string err_text(const char *what) { return std::string(what) + ": " + std::strerror(errno); }

} // namespace

std::optional<Parsed> parse_authorized_line(std::string_view in)
{
	if (in.ends_with('\r')) in.remove_suffix(1);
	in = trim_space_tab(in);
	if (in.empty() || in[0] == '#') return std::nullopt;
	size_t i = in.find_first_of(" \t");
	if (i == std::string_view::npos) return std::nullopt;
	if (auto p = parse_key_and_comment(in.substr(i)); p && in.substr(0, i) == p->key.type) return p;
	// Maybe an options field first.
	bool in_quote = false;
	std::vector<std::string> options;
	size_t option_start = 0;
	size_t k = 0;
	for (; k < in.size(); k++) {
		char b = in[k];
		bool is_end = !in_quote && (b == ' ' || b == '\t');
		if ((b == ',' && !in_quote) || is_end) {
			if (k > option_start) options.emplace_back(in.substr(option_start, k - option_start));
			option_start = k + 1;
		}
		if (is_end) break;
		if (b == '"' && (k == 0 || in[k - 1] != '\\')) in_quote = !in_quote;
	}
	// A range loop that ran out leaves its index at the last byte.
	if (k == in.size()) k = in.size() - 1;
	while (k < in.size() && (in[k] == ' ' || in[k] == '\t')) k++;
	if (k == in.size()) return std::nullopt;
	in = in.substr(k);
	i = in.find_first_of(" \t");
	if (i == std::string_view::npos) return std::nullopt;
	auto p = parse_key_and_comment(in.substr(i));
	if (!p || in.substr(0, i) != p->key.type) return std::nullopt;
	p->options = std::move(options);
	return p;
}

std::string fingerprint(const PublicKey &key)
{
	unsigned char sum[32];
	unsigned int len = 0;
	EVP_Digest(key.wire.data(), key.wire.size(), sum, &len, EVP_sha256(), nullptr);
	std::string b64 = base64::encode(std::string_view(reinterpret_cast<char *>(sum), len));
	while (!b64.empty() && b64.back() == '=') b64.pop_back();
	return "SHA256:" + b64;
}

Outcome import_keys(const std::string &home, std::string_view input, Result &result, std::string &error)
{
	result = Result{};
	std::vector<Entry> entries;
	if (!parse_input(input, entries)) return Outcome::invalid;
	// Directory descriptors without following links: no symlink leads the write elsewhere.
	int h = ::open(home.c_str(), O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
	if (h < 0) {
		error = err_text("open home");
		return Outcome::failed;
	}
	struct Close {
		int fd;
		~Close() { ::close(fd); }
	} close_h{h};
	if (::mkdirat(h, ".ssh", 0700) != 0 && errno != EEXIST) {
		error = err_text("mkdir .ssh");
		return Outcome::failed;
	}
	int d = ::openat(h, ".ssh", O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC);
	if (d < 0) {
		error = err_text("open .ssh");
		return Outcome::failed;
	}
	Close close_d{d};
	// Imports wait for each other, across threads and processes, without a lock file.
	if (::flock(d, LOCK_EX) != 0) {
		error = err_text("lock .ssh");
		return Outcome::failed;
	}
	struct Unlock {
		int fd;
		~Unlock() { ::flock(fd, LOCK_UN); }
	} unlock{d};
	std::string existing;
	int fd = ::openat(d, "authorized_keys", O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC);
	if (fd >= 0) {
		Close close_f{fd};
		struct stat st{};
		if (::fstat(fd, &st) != 0 || !S_ISREG(st.st_mode)) {
			error = "authorized_keys is not a regular file";
			return Outcome::failed;
		}
		char buf[8192];
		for (;;) {
			ssize_t n = ::read(fd, buf, sizeof buf);
			if (n < 0 && errno == EINTR) continue;
			if (n < 0) {
				error = err_text("read authorized_keys");
				return Outcome::failed;
			}
			if (n == 0) break;
			existing.append(buf, static_cast<size_t>(n));
			if (existing.size() > max_file) {
				error = "authorized_keys is too large";
				return Outcome::failed;
			}
		}
	} else if (errno != ENOENT) {
		error = err_text("open authorized_keys");
		return Outcome::failed;
	}
	std::set<std::string> seen;
	for (size_t pos = 0;;) {
		size_t nl = existing.find('\n', pos);
		auto p = parse_authorized_line(std::string_view(existing).substr(pos, nl == std::string::npos ? std::string::npos : nl - pos));
		if (p) seen.insert(p->key.wire);
		if (nl == std::string::npos) break;
		pos = nl + 1;
	}
	std::string added;
	for (const auto &item : entries) {
		if (seen.count(item.key.wire)) {
			result.skipped++;
			continue;
		}
		seen.insert(item.key.wire);
		result.added++;
		result.fingerprints.push_back(fingerprint(item.key));
		added += item.line + "\n";
	}
	if (result.added == 0) return Outcome::ok;
	std::string payload = existing;
	if (!payload.empty() && payload.back() != '\n') payload += '\n';
	payload += added;
	if (payload.size() > max_file) {
		result = Result{};
		error = "authorized_keys is too large";
		return Outcome::failed;
	}
	std::string name = ".authorized_keys-" + random_text();
	int tmp = ::openat(d, name.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0600);
	if (tmp < 0) {
		result = Result{};
		error = err_text("create");
		return Outcome::failed;
	}
	struct Unlink {
		int dir;
		std::string name;
		~Unlink() { ::unlinkat(dir, name.c_str(), 0); }
	} unlink_tmp{d, name};
	bool ok = ::fchmod(tmp, 0600) == 0;
	for (size_t done = 0; ok && done < payload.size();) {
		ssize_t n = ::write(tmp, payload.data() + done, payload.size() - done);
		if (n < 0 && errno == EINTR) continue;
		ok = n > 0;
		if (ok) done += static_cast<size_t>(n);
	}
	ok = ok && ::fsync(tmp) == 0;
	ok = ::close(tmp) == 0 && ok;
	ok = ok && ::fchmod(d, 0700) == 0 && ::renameat(d, name.c_str(), d, "authorized_keys") == 0 && ::fsync(d) == 0 &&
	     ::fsync(h) == 0;
	if (!ok) {
		result = Result{};
		error = err_text("write authorized_keys");
		return Outcome::failed;
	}
	return Outcome::ok;
}

std::string result_json(const Result &result)
{
	std::string out = "{\"added\":" + std::to_string(result.added) + ",\"skipped\":" + std::to_string(result.skipped) +
	                  ",\"fingerprints\":[";
	for (size_t i = 0; i < result.fingerprints.size(); i++) out += (i ? "," : "") + gojson::quote(result.fingerprints[i]);
	return out + "]}";
}

namespace {

void answer(std::uint64_t id, const std::vector<std::pair<std::string, std::string>> &headers, int code, std::string_view msg,
            const std::string &data = "")
{
	http::Response out;
	out.headers = headers;
	out.json(200, "{\"code\":" + std::to_string(code) + ",\"msg\":" + gojson::quote(msg) + ",\"data\":" +
	                  (data.empty() ? "null" : data) + "}");
	http::complete(id, std::move(out));
}

} // namespace

void add_routes(http::Router &router)
{
	router.add("POST", "/api/vm/ssh/keys/import", auth::guarded([](http::Request &req, http::Response &res) {
		// {"keys": "...", "password": "..."}; JSON field names match without regard to case, as encoding/json does.
		std::optional<std::string> keys, password;
		bool ok = req.body.size() <= max_input * 6 + 4096;
		glz::generic value;
		if (ok && glz::read_json(value, req.body)) ok = false;
		if (ok && !value.get_if<std::nullptr_t>()) {
			auto *object = value.get_if<glz::generic::object_t>();
			if (!object) ok = false;
			for (auto it = object ? object->begin() : decltype(object->begin()){}; ok && object && it != object->end(); ++it) {
				std::string key = it->first;
				for (auto &c : key) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
				std::optional<std::string> *target = key == "keys" ? &keys : key == "password" ? &password : nullptr;
				if (!target || it->second.get_if<std::nullptr_t>()) continue;
				if (auto *s = it->second.get_if<std::string>()) *target = *s;
				else ok = false;
			}
		}
		if (!ok) return res.json(200, R"({"code":-1,"msg":"invalid SSH key import request","data":null})");
		if (!req.auth || req.auth->role != "admin") {
			res.status = 403;
			res.done = true;
			return;
		}
		res.pending = true;
		loop::work([id = req.id, headers = res.headers, username = req.auth->username, remote = req.peer,
		            keys = keys.value_or(""), password = password.value_or("")] {
			std::string error;
			auto user = auth::store().authenticate(username, password, error);
			if (!error.empty() || !user || user->role != "admin") {
				log::info("SSH key import denied: user=\"{}\" remote=\"{}\"", username, remote);
				return answer(id, headers, -3, "auth.invalidCurrentPassword");
			}
			Result result;
			std::string failure;
			Outcome outcome = import_keys("/root", keys, result, failure);
			std::string fps = "[";
			for (size_t i = 0; i < result.fingerprints.size(); i++) fps += (i ? " " : "") + result.fingerprints[i];
			log::info("SSH key import: user=\"{}\" remote=\"{}\" added={} skipped={} fingerprints={}] success={}", username, remote,
			          result.added, result.skipped, fps, outcome == Outcome::ok);
			if (outcome == Outcome::invalid) return answer(id, headers, -1, "sshKeys.invalid");
			if (outcome == Outcome::failed) {
				log::error("SSH key import failed: {}", failure);
				return answer(id, headers, -2, "sshKeys.failed");
			}
			answer(id, headers, 0, "success", result_json(result));
		});
	}, auth::Guard{{"admin"}}));
}

} // namespace nkos::system::ssh_keys
