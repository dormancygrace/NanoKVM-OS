#include "config/config.hpp"

#include "common/log.hpp"
#include "config/yaml.hpp"

#include <openssl/rand.h>

#include <cerrno>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <optional>
#include <string>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>

namespace nkos::config {

namespace {

Config instance;
bool loaded = false;

// yaml.v3's Marshal of the Go defaultConfig (indent 4), the file a new device gets.
constexpr std::string_view default_yaml =
	"proto: https\n"
	"host: \"\"\n"
	"port:\n"
	"    http: 80\n"
	"    https: 443\n"
	"cert:\n"
	"    crt: /etc/kvm/server.crt\n"
	"    key: /etc/kvm/server.key\n"
	"logger:\n"
	"    level: info\n"
	"    file: stdout\n"
	"authentication: enable\n"
	"jwt:\n"
	"    secretKey: \"\"\n"
	"    refreshTokenDuration: 2678400\n"
	"    revokeTokensOnLogout: true\n"
	"stun: stun.l.google.com:19302,stun.cloudflare.com:3478\n"
	"turn:\n"
	"    turnAddr: \"\"\n"
	"    turnUser: \"\"\n"
	"    turnCred: \"\"\n"
	"dtlsCipher: \"\"\n"
	"security:\n"
	"    loginLockoutDuration: 300\n"
	"    loginMaxFailures: 5\n"
	"    trustedProxies:\n"
	"        - 127.0.0.1/32\n"
	"        - ::1/128\n"
	"    allowedOrigins: []\n";

std::optional<std::string> read_file(const std::string &path)
{
	FILE *f = std::fopen(path.c_str(), "rb");
	if (!f) return std::nullopt;
	std::string data;
	char buf[4096];
	size_t n;
	while ((n = std::fread(buf, 1, sizeof buf, f)) > 0) data.append(buf, n);
	std::fclose(f);
	return data;
}

bool is_regular(const std::string &path)
{
	struct stat st{};
	return ::stat(path.c_str(), &st) == 0 && !S_ISDIR(st.st_mode);
}

// Viper's search: server.json, server.toml, server.yaml, ... in this order, all read as YAML (JSON is YAML).
std::optional<std::string> read_configuration(bool &missing)
{
	static const char *const suffixes[] = {".json", ".toml", ".yaml", ".yml", ".properties", ".props", ".prop",
	                                       ".hcl", ".tfvars", ".dotenv", ".env", ".ini", ""};
	std::string base = configuration_file;
	base.erase(base.rfind('.'));
	missing = true;
	for (const char *suffix : suffixes) {
		std::string path = base + suffix;
		if (!is_regular(path)) continue;
		missing = false;
		return read_file(path);
	}
	return std::nullopt;
}

void create_default()
{
	std::string dir = configuration_file;
	dir.erase(dir.rfind('/'));
	::mkdir(dir.c_str(), 0755);
	if (!atomic_write(configuration_file, default_yaml, 0600)) {
		log::warn("failed to save config: {}", std::strerror(errno));
		return;
	}
	log::info("create file {} with default configuration", configuration_file);
}

// base64.URLEncoding of 64 random bytes, as generateRandomSecretKey.
std::string random_secret()
{
	unsigned char b[64];
	if (RAND_bytes(b, sizeof b) != 1) {
		timespec ts{};
		clock_gettime(CLOCK_REALTIME, &ts);
		char t[80];
		std::snprintf(t, sizeof t, "%064lld", static_cast<long long>(ts.tv_sec) * 1000000000LL + ts.tv_nsec);
		return t;
	}
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
	std::string out;
	for (size_t i = 0; i < sizeof b; i += 3) {
		unsigned v = b[i] << 16 | (i + 1 < sizeof b ? b[i + 1] << 8 : 0) | (i + 2 < sizeof b ? b[i + 2] : 0);
		out += alphabet[v >> 18 & 63];
		out += alphabet[v >> 12 & 63];
		out += i + 1 < sizeof b ? alphabet[v >> 6 & 63] : '=';
		out += i + 2 < sizeof b ? alphabet[v & 63] : '=';
	}
	return out;
}

std::string trim(std::string s)
{
	auto space = [](char c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'; };
	while (!s.empty() && space(s.back())) s.pop_back();
	size_t i = 0;
	while (i < s.size() && space(s[i])) i++;
	return s.substr(i);
}

// loadOrCreateJWTSecret: an existing non-empty secret is kept (mode forced to 0600); otherwise a new one is written.
std::optional<std::string> load_or_create_jwt_secret(const std::string &path)
{
	if (auto data = read_file(path)) {
		std::string secret = trim(*data);
		if (!secret.empty()) {
			if (::chmod(path.c_str(), 0600) != 0) return std::nullopt;
			return secret;
		}
	} else if (errno != ENOENT) {
		return std::nullopt;
	}
	std::string secret = random_secret();
	if (!atomic_write(path, secret + "\n", 0600)) return std::nullopt;
	return secret;
}

void decode(const yaml::Value &root, Config &c)
{
	using namespace yaml;
	if (auto v = as_string(root.get("proto"))) c.proto = *v;
	if (auto v = as_string(root.get("host"))) c.host = *v;
	if (auto v = as_int(root.path("port.http"))) c.port.http = static_cast<int>(*v);
	if (auto v = as_int(root.path("port.https"))) c.port.https = static_cast<int>(*v);
	if (auto v = as_string(root.path("cert.crt"))) c.cert.crt = *v;
	if (auto v = as_string(root.path("cert.key"))) c.cert.key = *v;
	if (auto v = as_string(root.path("logger.level"))) c.logger.level = *v;
	if (auto v = as_string(root.path("logger.file"))) c.logger.file = *v;
	if (auto v = as_string(root.get("authentication"))) c.authentication = *v;
	if (auto v = as_string(root.path("jwt.secretkey"))) c.jwt.secret_key = *v;
	if (auto v = as_uint(root.path("jwt.refreshtokenduration"))) c.jwt.refresh_token_duration = *v;
	if (auto v = as_bool(root.path("jwt.revoketokensonlogout"))) c.jwt.revoke_tokens_on_logout = *v;
	if (auto v = as_string(root.get("stun"))) c.stun = *v;
	if (auto v = as_string(root.path("turn.turnaddr"))) c.turn.turn_addr = *v;
	if (auto v = as_string(root.path("turn.turnuser"))) c.turn.turn_user = *v;
	if (auto v = as_string(root.path("turn.turncred"))) c.turn.turn_cred = *v;
	if (auto v = as_string(root.get("dtlscipher"))) c.dtls_cipher = *v;
	if (auto v = as_int(root.path("security.loginlockoutduration"))) c.security.login_lockout_duration = static_cast<int>(*v);
	if (auto v = as_int(root.path("security.loginmaxfailures"))) c.security.login_max_failures = static_cast<int>(*v);
	if (auto v = as_strings(root.path("security.trustedproxies"))) c.security.trusted_proxies = *v;
	else c.security.trusted_proxies_set = false;
	if (auto v = as_strings(root.path("security.allowedorigins"))) c.security.allowed_origins = *v;
}

// checkDefaultValue.
void fix_defaults(Config &c)
{
	if (c.jwt.secret_key.empty()) {
		auto secret = load_or_create_jwt_secret(jwt_secret_file);
		if (!secret) {
			log::warn("failed to persist JWT secret: {}", std::strerror(errno));
			secret = random_secret();
		}
		c.jwt.secret_key = *secret;
		c.jwt.revoke_tokens_on_logout = true;
	}
	if (c.jwt.refresh_token_duration == 0) c.jwt.refresh_token_duration = 2678400;
	if (c.stun.empty() || c.stun == "stun.l.google.com:19302") c.stun = default_stun;
	if (c.authentication.empty()) c.authentication = "enable";
	if (c.security.login_max_failures <= 0) c.security.login_max_failures = 5;
	if (!c.security.trusted_proxies_set) c.security.trusted_proxies = {"127.0.0.1/32", "::1/128"};
}

} // namespace

Cert Cert::with_defaults() const
{
	Cert c = *this;
	if (c.crt.empty()) c.crt = default_cert_file;
	if (c.key.empty()) c.key = default_key_file;
	return c;
}

bool atomic_write(const std::string &path, std::string_view data, unsigned mode)
{
	std::string dir = path.substr(0, path.rfind('/'));
	std::string name = path.substr(path.rfind('/') + 1);
	std::string tmp = dir + "/." + name + ".tmp-XXXXXX";
	int fd = ::mkstemp(tmp.data());
	if (fd < 0) return false;
	bool ok = ::fchmod(fd, mode) == 0;
	for (size_t done = 0; ok && done < data.size();) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		ok = n > 0;
		if (ok) done += static_cast<size_t>(n);
	}
	ok = ok && ::fsync(fd) == 0;
	ok = ::close(fd) == 0 && ok;
	ok = ok && ::rename(tmp.c_str(), path.c_str()) == 0;
	if (!ok) {
		int saved = errno;
		::unlink(tmp.c_str());
		errno = saved;
		return false;
	}
	int d = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (d >= 0) {
		ok = ::fsync(d) == 0;
		::close(d);
	}
	return ok;
}

const Config &load()
{
	if (loaded) return instance;
	loaded = true;
	bool missing = false;
	std::string error;
	std::optional<yaml::Value> values;
	if (auto text = read_configuration(missing)) values = yaml::parse(*text, error);
	if (!values) {
		if (missing) create_default();
		values = yaml::parse(default_yaml, error);
		log::info("using default configuration");
	}
	// validate: positive HTTP and HTTPS ports, or the file is set aside and the defaults are used.
	auto http = yaml::as_int(values->path("port.http"));
	auto https = yaml::as_int(values->path("port.https"));
	if (!http || !https || *http <= 0 || *https <= 0) {
		if (is_regular(configuration_file)) {
			std::string bad = std::string(configuration_file) + ".bad";
			if (::rename(configuration_file, bad.c_str()) != 0)
				log::warn("failed to set aside invalid configuration: {}", std::strerror(errno));
			else
				log::info("invalid configuration moved to {}; using defaults", bad);
		}
		create_default();
		values = yaml::parse(default_yaml, error);
	}
	decode(*values, instance);
	fix_defaults(instance);
	if (instance.authentication == "disable")
		log::warn("NOTICE: Authentication is disabled! Please ensure your service is secure!");
	log::info("config loaded successfully");
	return instance;
}

const Config &get() { return load(); }

} // namespace nkos::config
