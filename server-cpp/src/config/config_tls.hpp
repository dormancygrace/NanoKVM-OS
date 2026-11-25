#pragma once

// The TLS keys of server.yaml as the Go server's config.Read and config.UpdateTLS handle them: read fresh from the
// file (not the configuration loaded at start), and written back with every other key, comment and unknown field
// left as it was.

#include "config/config.hpp"

#include <mutex>
#include <optional>
#include <string>

namespace nkos::config {

struct TlsSettings {
	std::string proto;  // "" when absent
	Cert cert;          // "" paths when absent (Cert::with_defaults fills them)
};

// config.Read for proto and cert: nullopt when the file cannot be read or is not valid YAML.
std::optional<TlsSettings> read_tls(const std::string &path = configuration_file);

// The lock of every read-modify-write of server.yaml (the Go server's configurationFileMu): the TLS edit takes it,
// and so must any other edit of the file.
std::mutex &server_yaml_mutex();

// config.UpdateTLS: sets proto, cert.crt and cert.key (adding the keys that are missing) and writes the file
// atomically (0600). false with error set when the file cannot be read, parsed or written.
bool update_tls(const std::string &proto, const Cert &cert, std::string &error, const std::string &path = configuration_file);

} // namespace nkos::config
