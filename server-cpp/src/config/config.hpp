#pragma once

// The server configuration, /etc/kvm/server.yaml, read as the Go server reads it (server/config): the same file
// search, defaults, validation, fix-ups and the persistent JWT secret.

#include <cstdint>
#include <string>
#include <vector>

namespace nkos::config {

inline constexpr const char *configuration_file = "/etc/kvm/server.yaml";
inline constexpr const char *jwt_secret_file = "/etc/kvm/.jwt_secret";
inline constexpr const char *default_cert_file = "/etc/kvm/server.crt";
inline constexpr const char *default_key_file = "/etc/kvm/server.key";
inline constexpr const char *default_stun = "stun.l.google.com:19302,stun.cloudflare.com:3478";

struct Port {
	int http = 80;
	int https = 443;
};

struct Cert {
	std::string crt = default_cert_file;
	std::string key = default_key_file;
	Cert with_defaults() const;  // empty paths are the defaults
};

struct Logger {
	std::string level = "info";
	std::string file = "stdout";
};

struct JWT {
	std::string secret_key;
	std::uint64_t refresh_token_duration = 2678400;
	bool revoke_tokens_on_logout = true;
};

struct Turn {
	std::string turn_addr, turn_user, turn_cred;
};

struct Security {
	int login_lockout_duration = 300;
	int login_max_failures = 5;
	std::vector<std::string> trusted_proxies{"127.0.0.1/32", "::1/128"};
	bool trusted_proxies_set = true;
	std::vector<std::string> allowed_origins;
};

struct Config {
	std::string proto = "https";
	std::string host;
	Port port;
	Cert cert;
	Logger logger;
	std::string authentication = "enable";
	JWT jwt;
	std::string stun = default_stun;
	Turn turn;
	std::string dtls_cipher;
	Security security;
};

// Loads the configuration once at start (the Go server's GetInstance): reads the file, creates it with the
// defaults when missing, sets an invalid one aside as server.yaml.bad, and applies the default fix-ups.
const Config &load();
const Config &get();

// Writes data atomically: temporary file in the same directory, mode, fsync, rename, fsync of the directory.
bool atomic_write(const std::string &path, std::string_view data, unsigned mode);

} // namespace nkos::config
