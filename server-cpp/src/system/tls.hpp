#pragma once

// HTTPS settings, as the Go server's service/vm tls.go and tls_cert.go with utils/cert.go and certstore.go: the
// device's generated certificate pair, a custom (uploaded) or ACME pair named by server.yaml, the pair the HTTPS
// listener serves (switched at run time without a restart), and the routes /api/vm/tls*.

#include "config/config.hpp"
#include "config/config_tls.hpp"
#include "http/router.hpp"
#include "system/tls_x509.hpp"

#include <functional>
#include <memory>
#include <mutex>
#include <optional>
#include <string>

namespace nkos::system::tls {

struct Paths {
	std::string default_crt = config::default_cert_file, default_key = config::default_key_file;
	std::string custom_crt = "/etc/kvm/tls/custom.crt", custom_key = "/etc/kvm/tls/custom.key";
	std::string acme_crt = "/etc/kvm/tls/acme.crt", acme_key = "/etc/kvm/tls/acme.key";
};

// utils.ServerCertificate: the pair the HTTPS listener presents. A failed load keeps the previous pair.
struct Served {
	std::shared_ptr<const Parsed> pair;
	std::string crt, key;
	std::string error;     // the last failed load, "" after a successful one
	std::string fallback;  // why the generated pair stands in for the configured one
};
class Store {
public:
	// Reads and checks the pair (utils.LoadCertificatePair) and makes it the served one: "" or the error.
	std::string load(const std::string &crt, const std::string &key);
	void mark_fallback(std::string why);
	Served current() const;
	// Installs a pair in the running listener; unset until the server runs. Called off the event loop.
	std::function<bool(const Parsed &)> serve;

private:
	mutable std::mutex mutex_;
	Served served_;
};
Store &server_certificate();

// utils.EnsureServerCertificate and utils.PrepareServerCertificate: "" or the error.
std::string ensure_server_certificate(const std::string &crt, const std::string &key, const Paths &paths, Instant at);
std::string prepare_server_certificate(const std::string &crt, const std::string &key, Store &store, const Paths &paths,
                                       Instant at);

// What the routes use, replaceable for checks (the Go service's package variables).
struct Env {
	Paths paths;
	std::function<std::optional<config::TlsSettings>()> read_config;
	std::function<bool(const std::string &proto, const config::Cert &cert)> update_config;
	Store *store = nullptr;
	std::function<Instant()> clock;  // the upload's validity check
	std::function<std::string()> ensure_generated;  // the generated pair exists and is valid: "" or the error
};
Env &env();

// A proto.Response: code, message and the data JSON ("" is null).
struct Answer {
	int code = 0;
	std::string msg = "success";
	std::string data;
};
std::string answer_json(const Answer &a);

// The route bodies (under the service's lock).
Answer get_status(Env &e);
Answer upload(Env &e, std::string_view cert_pem, std::string_view key_pem);
Answer remove(Env &e);
Answer reload(Env &e);
// enableTls / disableTls: "" or the error.
std::string set_tls(Env &e, bool enabled);

// At start, before http::run: when HTTPS is on, PrepareServerCertificate for the configured pair, and the pair the
// listener starts with (http::use_certificate_files). The error ends the start, as Go's panic does.
std::optional<std::string> prepare(const config::Config &conf);
// Once the listeners run (loop thread): later loads go to the listener.
void start();

void add_routes(http::Router &router);

} // namespace nkos::system::tls
