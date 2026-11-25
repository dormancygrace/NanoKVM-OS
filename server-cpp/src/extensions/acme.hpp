#pragma once

// The HTTPS certificate from a public CA with acme.sh, as the Go server's service/extensions/acme: acme.sh installed
// from its latest stable GitHub release into /etc/kvm/acme/bin (script and DNS hooks only), its account, domain
// records and log in /etc/kvm/acme/data, issue and renewal as background jobs the page polls, the HTTP-01 tokens
// served on both listeners, a renewal check twice a day, and the issued pair (/etc/kvm/tls/acme.{crt,key}) put in
// use through the TLS settings.

#include "extensions/common.hpp"
#include "http/router.hpp"

#include <functional>
#include <optional>
#include <string>

namespace nkos::extensions::acme {

// The TLS side the Go service reaches through config and utils (its package variables readConfig, updateTLS,
// serverCertificate, ensureGenerated). Errors are Go's texts; "" is success.
struct TlsConfig {
	std::string proto;
	int http_port = 0;
	std::string crt, key;  // as written, "" when absent (the defaults apply)
};
struct Tls {
	// config.Read: server.yaml read fresh.
	std::function<std::optional<TlsConfig>()> read_config;
	// config.UpdateTLS.
	std::function<std::string(const std::string &proto, const std::string &crt, const std::string &key)> update_tls;
	// utils.ServerCertificate.Load: the pair read, checked and served.
	std::function<std::string(const std::string &crt, const std::string &key)> load;
	// The DER of the served leaf; nullopt when none is served.
	std::function<std::optional<std::string>()> served_leaf;
	// utils.LoadCertificatePair: the DER of the pair's leaf; nullopt when it does not load.
	std::function<std::optional<std::string>(const std::string &crt, const std::string &key)> pair_leaf;
	// utils.EnsureServerCertificate for the generated pair.
	std::function<std::string()> ensure_generated;
	// utils.DescribeCertificate of the pair as JSON (CertificateInfo); nullopt when it does not load.
	std::function<std::optional<std::string>(const std::string &crt, const std::string &key)> describe;
};
// Bound to server.yaml and the TLS settings (system/tls); replaceable by checks.
Tls &tls();

// The HTTP-01 path a CA fetches tokens from.
inline constexpr const char *challenge_prefix = "/.well-known/acme-challenge/";
// ChallengeHandler: the token files, and nothing else, without authentication (GET and HEAD).
void challenge(const http::Request &req, http::Response &res);

// The routes (/api/extensions/acme/...).
Reply get_status();
Reply get_providers();
Reply install();
Reply check_update();
Reply uninstall();
Reply issue(const std::string &body);
Reply renew();
Reply remove();

// RunRenewal: the selected certificate renewed when due, ten minutes after the start and then every 12 hours
// (a thread).
void start();

// For checks.
namespace check {
extern std::string root, challenge_root, cert_file, key_file, latest_url, tarball_base;
// renewDue, in the calling thread (the job still runs in the background).
void renew_due();
// Waits until no job runs.
void wait_job();
std::string script_version(const std::string &script);
bool newer_version(const std::string &installed, const std::string &candidate);
std::string providers_json(const std::string &dir);
std::string provider_json(const std::string &hook, const std::string &script);
// Settings.normalize of a settings JSON: the normalized JSON, or the error.
std::string normalize(const std::string &settings_json, std::string &error);
// issueArgs: argv, env and secrets joined by \n, or the error.
std::string issue_args(const std::string &request_json, std::string &error);
std::string scrub(const std::string &text, const std::vector<std::string> &secrets);
std::string tail_buffer(const std::vector<std::string> &writes);
std::int64_t steps_timeout(const std::vector<std::vector<std::string>> &steps, int dns_sleep);
} // namespace check

} // namespace nkos::extensions::acme
