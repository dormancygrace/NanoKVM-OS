#include "extensions/acme.hpp"

#include "common/http_client.hpp"
#include "common/log.hpp"
#include "config/config.hpp"
#include "config/yaml.hpp"
#include "extensions/gopath.hpp"
#include "network/util.hpp"
#include "osupdate/godecode.hpp"
#include "config/config_tls.hpp"
#include "system/tls.hpp"

#include <algorithm>
#include <cctype>
#include <cerrno>
#include <climits>
#include <condition_variable>
#include <csignal>
#include <cstring>
#include <dirent.h>
#include <fcntl.h>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <sys/stat.h>
#include <thread>
#include <unistd.h>
#include <zlib.h>

namespace nkos::extensions::acme {

namespace check {
std::string root = "/etc/kvm/acme";
std::string challenge_root = "/run/nanokvm/acme-challenge";
std::string cert_file = "/etc/kvm/tls/acme.crt";
std::string key_file = "/etc/kvm/tls/acme.key";
// The latest stable release (GitHub leaves drafts and pre-releases out of "latest") and the archive of a tag.
std::string latest_url = "https://api.github.com/repos/acmesh-official/acme.sh/releases/latest";
std::string tarball_base = "https://github.com/acmesh-official/acme.sh/archive/refs/tags/";
} // namespace check

namespace {

using check::cert_file;
using check::challenge_root;
using check::key_file;
using check::root;
using network::trim_space;
using K = godecode::Value::Kind;

constexpr std::size_t max_tarball = 4 << 20;
constexpr std::int64_t max_file = 1 << 20;
constexpr int job_timeout = 20 * 60;
constexpr int max_dns_sleep = 3600;
constexpr std::size_t output_limit = 64 * 1024;
constexpr int renew_skip = 2;
constexpr const char *log_name = "last-run.log";
constexpr const char *default_cert = "/etc/kvm/server.crt";
constexpr const char *default_key = "/etc/kvm/server.key";

std::string bin_dir() { return root + "/bin"; }
std::string data_dir() { return root + "/data"; }
std::string script() { return bin_dir() + "/acme.sh"; }
std::string hooks_dir() { return bin_dir() + "/dnsapi"; }
std::string cert_dir() { return gopath::dir(cert_file); }
std::string settings_file() { return data_dir() + "/nanokvm.json"; }

// One acme.sh operation at a time: install, a job, remove, uninstall.
std::mutex op_mu;

bool installed()
{
	struct stat st{};
	return ::stat(script().c_str(), &st) == 0 && S_ISREG(st.st_mode);
}

// --- matchers ---------------------------------------------------------------------------------------------------

bool digit(char c) { return c >= '0' && c <= '9'; }
bool lower_alnum(char c) { return (c >= 'a' && c <= 'z') || digit(c); }

// ^[0-9]+(\.[0-9]+){1,3}$
bool release_tag(std::string_view s)
{
	auto parts = network::split(s, ".");
	if (parts.size() < 2 || parts.size() > 4) return false;
	for (const auto &p : parts)
		if (p.empty() || !std::all_of(p.begin(), p.end(), digit)) return false;
	return true;
}

// ^dns_[a-z0-9_]+$
bool hook_name(std::string_view s)
{
	if (s.size() < 5 || s.substr(0, 4) != "dns_") return false;
	return std::all_of(s.begin() + 4, s.end(), [](char c) { return lower_alnum(c) || c == '_'; });
}

// ^[A-Za-z0-9_-]{1,128}$
bool token_ok(std::string_view s)
{
	if (s.empty() || s.size() > 128) return false;
	return std::all_of(s.begin(), s.end(), [](char c) {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || digit(c) || c == '_' || c == '-';
	});
}

// ^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$
bool domain_ok(std::string_view s)
{
	if (s.substr(0, 2) == "*.") s.remove_prefix(2);
	auto labels = network::split(s, ".");
	if (labels.size() < 2) return false;
	for (std::size_t i = 0; i < labels.size(); i++) {
		const auto &l = labels[i];
		if (l.empty() || l.size() > 63) return false;
		bool last = i + 1 == labels.size();
		if (last ? !(l[0] >= 'a' && l[0] <= 'z') : !lower_alnum(l[0])) return false;
		if (l.size() > 1 && !lower_alnum(l.back())) return false;
		for (char c : l)
			if (!lower_alnum(c) && c != '-') return false;
	}
	return true;
}

// ^[^@\s]+@[^@\s]+\.[^@\s]+$ with \s the ASCII white space of Go's regexp ([\t\n\f\r ]).
bool email_ok(std::string_view s)
{
	auto space = [](char c) { return c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '; };
	if (std::any_of(s.begin(), s.end(), space)) return false;
	auto at = s.find('@');
	if (at == std::string_view::npos || at == 0 || s.find('@', at + 1) != std::string_view::npos) return false;
	auto domain = s.substr(at + 1);
	for (std::size_t i = 1; i + 1 < domain.size(); i++)
		if (domain[i] == '.') return true;
	return false;
}

std::string to_lower_ascii(std::string s)
{
	for (auto &c : s) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	return s;
}

// --- versions ---------------------------------------------------------------------------------------------------

std::string script_version(std::string_view data)
{
	if (data.size() > 4096) data = data.substr(0, 4096);
	// (?m)^VER=(\S+): at the start of a line, then everything up to Go's ASCII white space.
	for (std::size_t i = 0; i < data.size();) {
		if (data.substr(i, 4) == "VER=") {
			std::size_t j = i + 4;
			while (j < data.size() && !std::strchr("\t\n\f\r ", data[j])) j++;
			if (j > i + 4) return trim_space(std::string(data.substr(i + 4, j - i - 4)));
		}
		auto nl = data.find('\n', i);
		if (nl == std::string_view::npos) break;
		i = nl + 1;
	}
	return "";
}

std::string installed_version()
{
	auto data = read_file(script());
	return data ? script_version(*data) : "";
}

// strconv.Atoi of a run of digits, at its range limit when too large.
std::int64_t atoi_clamped(const std::string &s)
{
	std::int64_t n = 0;
	for (char c : s) {
		if (n > (INT64_MAX - (c - '0')) / 10) return INT64_MAX;
		n = n * 10 + (c - '0');
	}
	return n;
}

bool newer_version(const std::string &installed, const std::string &candidate)
{
	if (!release_tag(candidate)) return false;
	if (!release_tag(installed)) return true;
	auto a = network::split(installed, "."), b = network::split(candidate, ".");
	for (std::size_t i = 0; i < a.size() || i < b.size(); i++) {
		std::int64_t x = i < a.size() ? atoi_clamped(a[i]) : 0, y = i < b.size() ? atoi_clamped(b[i]) : 0;
		if (x != y) return y > x;
	}
	return false;
}

// --- settings ---------------------------------------------------------------------------------------------------

struct Settings {
	std::optional<std::vector<std::string>> domains;
	std::string email, ca, challenge, dns_provider;
	std::int64_t dns_sleep = 0;
	std::string issued_at;
	std::string primary() const { return domains && !domains->empty() ? (*domains)[0] : ""; }
};

const std::map<std::string, std::string> ca_servers{{"buypass", "buypass"},       {"google", "google"},
                                                    {"google_test", "googletest"}, {"letsencrypt", "letsencrypt"},
                                                    {"letsencrypt_test", "letsencrypt_test"}, {"zerossl", "zerossl"}};

// A settings member of the JSON (the Settings fields): false on a value of another type; unknown keys are ignored.
bool settings_member(Settings &s, const std::string &k, const godecode::Value &v, bool &known)
{
	known = true;
	if (godecode::key_matches(k, "domains")) return godecode::get_strings(v, s.domains);
	if (godecode::key_matches(k, "email")) return godecode::get(v, s.email);
	if (godecode::key_matches(k, "ca")) return godecode::get(v, s.ca);
	if (godecode::key_matches(k, "challenge")) return godecode::get(v, s.challenge);
	if (godecode::key_matches(k, "dnsProvider")) return godecode::get(v, s.dns_provider);
	if (godecode::key_matches(k, "dnsSleep")) return godecode::get_int(v, s.dns_sleep, INT64_MIN, INT64_MAX);
	if (godecode::key_matches(k, "issuedAt")) return godecode::get(v, s.issued_at);
	known = false;
	return true;
}

std::optional<Settings> load_settings()
{
	auto data = read_file(settings_file());
	if (!data) return std::nullopt;
	auto doc = godecode::parse_document(*data);
	if (!doc) return std::nullopt;
	Settings s;
	if (doc->kind == K::null) return s;
	if (doc->kind != K::object) return std::nullopt;
	bool good = true, known = false;
	for (const auto &[k, v] : doc->members) good = settings_member(s, k, v, known) && good;
	if (!good) return std::nullopt;
	return s;
}

std::string settings_json(const Settings &s, bool indent)
{
	std::string nl = indent ? "\n  " : "", sep = indent ? ": " : ":";
	std::string domains = "null";
	if (s.domains) {
		if (s.domains->empty()) {
			domains = "[]";
		} else {
			domains = "[";
			for (std::size_t i = 0; i < s.domains->size(); i++)
				domains += std::string(i ? "," : "") + (indent ? "\n    " : "") + q((*s.domains)[i]);
			domains += (indent ? "\n  ]" : "]");
		}
	}
	std::string out = "{" + nl + "\"domains\"" + sep + domains;
	auto field = [&](const char *name, const std::string &value) { out += "," + nl + "\"" + name + "\"" + sep + value; };
	if (!s.email.empty()) field("email", q(s.email));
	field("ca", q(s.ca));
	field("challenge", q(s.challenge));
	if (!s.dns_provider.empty()) field("dnsProvider", q(s.dns_provider));
	if (s.dns_sleep != 0) field("dnsSleep", std::to_string(s.dns_sleep));
	if (!s.issued_at.empty()) field("issuedAt", q(s.issued_at));
	return out + (indent ? "\n}" : "}");
}

std::string save_settings(const Settings &s)
{
	if (auto e = gopath::mkdir_all(data_dir(), 0700); !e.empty()) return e;
	auto e = network::atomicfile_write(settings_file(), settings_json(s, true), 0600);
	return e.text;
}

// normalize: the domains lowercased and trimmed, the settings checked.
std::string normalize(Settings &s)
{
	if (!s.domains || s.domains->empty() || s.domains->size() > 10) return "give 1 to 10 domains";
	std::set<std::string> seen;
	for (auto &raw : *s.domains) {
		std::string d = to_lower_ascii(trim_space(raw));
		if (d.size() > 253 || !domain_ok(d) || network::parse_ip(d)) return go_quote(d) + " is not a valid domain name";
		if (seen.count(d)) return go_quote(d) + " is listed twice";
		seen.insert(d);
		raw = d;
	}
	s.email = trim_space(s.email);
	if (!s.email.empty() && (s.email.size() > 254 || !email_ok(s.email))) return "the e-mail address is not valid";
	if (!ca_servers.count(s.ca)) return "unknown certificate authority " + go_quote(s.ca);
	if (s.ca == "zerossl" && s.email.empty()) return "ZeroSSL needs an e-mail address to register the account";
	if (s.challenge == "http") {
		for (const auto &d : *s.domains)
			if (d.rfind("*.", 0) == 0) return "a wildcard certificate needs the DNS challenge";
		s.dns_provider.clear();
		s.dns_sleep = 0;
	} else if (s.challenge == "dns") {
		if (!hook_name(s.dns_provider)) return "choose a DNS provider";
		if (s.dns_sleep < 0 || s.dns_sleep > max_dns_sleep) return "the DNS wait must be 0 to 3600 seconds";
	} else {
		return "the challenge must be http or dns";
	}
	return "";
}

// --- providers --------------------------------------------------------------------------------------------------

struct Option {
	std::string name, description;
};
struct Provider {
	std::string hook, name, site, docs;
	std::vector<Option> options, options_alt;
};

std::string with_scheme(const std::string &url)
{
	if (url.empty() || url.find("://") != std::string::npos) return url;
	return "https://" + url;
}

// bufio.Scanner lines: \n separated, a trailing \r dropped, stopping at a line longer than 64 KiB.
std::vector<std::string> scan_lines(std::string_view text)
{
	std::vector<std::string> lines;
	std::size_t i = 0;
	while (i < text.size()) {
		auto nl = text.find('\n', i);
		std::size_t end = nl == std::string_view::npos ? text.size() : nl;
		if (end - i > 64 * 1024 - 1 && nl != std::string_view::npos) break;
		if (nl == std::string_view::npos && end - i > 64 * 1024) break;
		std::string line(text.substr(i, end - i));
		if (!line.empty() && line.back() == '\r') line.pop_back();
		lines.push_back(line);
		if (nl == std::string_view::npos) break;
		i = nl + 1;
	}
	return lines;
}

bool cut(std::string_view s, char sep, std::string &before, std::string &after)
{
	auto i = s.find(sep);
	if (i == std::string_view::npos) {
		before = s;
		after.clear();
		return false;
	}
	before = s.substr(0, i);
	after = s.substr(i + 1);
	return true;
}

Provider parse_provider_info(const std::string &hook, std::string_view script)
{
	Provider p{hook, hook, "", "", {}, {}};
	std::string marker = hook + "_info='";
	auto start = script.find(marker);
	if (start == std::string_view::npos) return p;
	auto block = script.substr(start + marker.size());
	auto end = block.find('\'');
	if (end == std::string_view::npos) return p;
	bool first = true;
	std::string section;
	for (const auto &line : scan_lines(block.substr(0, end))) {
		if (first) {
			first = false;
			if (auto name = trim_space(line); !name.empty()) p.name = name;
			continue;
		}
		if (!line.empty() && line[0] == ' ') {
			std::string name, description;
			cut(trim_space(line), ' ', name, description);
			if (name.empty()) continue;
			Option o{name, trim_space(description)};
			if (section == "Options") p.options.push_back(o);
			else if (section == "OptionsAlt") p.options_alt.push_back(o);
			continue;
		}
		std::string key, value;
		if (!cut(line, ':', key, value)) continue;
		key = trim_space(key);
		value = trim_space(value);
		section.clear();
		if (key == "Options" || key == "OptionsAlt") section = key;
		else if (key == "Site") p.site = with_scheme(value);
		else if (key == "Docs") p.docs = with_scheme(value);
	}
	return p;
}

std::string options_json(const std::vector<Option> &options)
{
	std::string out = "[";
	for (std::size_t i = 0; i < options.size(); i++)
		out += std::string(i ? "," : "") + "{\"name\":" + q(options[i].name) + ",\"description\":" + q(options[i].description) + "}";
	return out + "]";
}

std::string provider_json(const Provider &p)
{
	return "{\"hook\":" + q(p.hook) + ",\"name\":" + q(p.name) + (p.site.empty() ? "" : ",\"site\":" + q(p.site)) +
	       (p.docs.empty() ? "" : ",\"docs\":" + q(p.docs)) + ",\"options\":" + options_json(p.options) +
	       (p.options_alt.empty() ? "" : ",\"optionsAlt\":" + options_json(p.options_alt)) + "}";
}

// listProviders: every dns_*.sh of the directory (sorted by name, as Glob returns them), ordered by display name.
bool list_providers(const std::string &dir, std::vector<Provider> &out)
{
	std::vector<std::string> names;
	if (DIR *d = ::opendir(dir.c_str())) {
		while (dirent *e = ::readdir(d)) {
			std::string n = e->d_name;
			if (n.size() >= 7 && n.rfind("dns_", 0) == 0 && n.compare(n.size() - 3, 3, ".sh") == 0) names.push_back(n);
		}
		::closedir(d);
	}
	std::sort(names.begin(), names.end());
	for (const auto &n : names) {
		std::string hook = n.substr(0, n.size() - 3);
		if (!hook_name(hook)) continue;
		auto data = read_file(dir + "/" + n);
		if (!data) return false;
		out.push_back(parse_provider_info(hook, *data));
	}
	std::stable_sort(out.begin(), out.end(),
	                 [](const Provider &a, const Provider &b) { return to_lower_ascii(a.name) < to_lower_ascii(b.name); });
	return true;
}

std::optional<Provider> provider(const std::string &hook)
{
	if (!hook_name(hook)) return std::nullopt;
	auto data = read_file(hooks_dir() + "/" + hook + ".sh");
	if (!data) return std::nullopt;
	return parse_provider_info(hook, *data);
}

// --- TLS --------------------------------------------------------------------------------------------------------

std::optional<TlsConfig> read_config()
{
	auto &t = tls();
	if (!t.read_config) return std::nullopt;
	return t.read_config();
}

bool in_use(const TlsConfig &conf)
{
	std::string crt = conf.crt.empty() ? default_cert : conf.crt, key = conf.key.empty() ? default_key : conf.key;
	return crt == cert_file && key == key_file;
}

const std::string unwired = "the TLS settings are not available";

std::string load_served(const std::string &crt, const std::string &key)
{
	return tls().load ? tls().load(crt, key) : unwired;
}

std::string update_tls(const std::string &proto, const std::string &crt, const std::string &key)
{
	return tls().update_tls ? tls().update_tls(proto, crt, key) : unwired;
}

// activate: the configuration points at the ACME pair, which is served.
std::string activate()
{
	auto conf = read_config();
	if (!conf) return "cannot read the configuration";
	if (!in_use(*conf))
		if (auto e = update_tls(conf->proto, cert_file, key_file); !e.empty()) return e;
	if (conf->proto == "https") return load_served(cert_file, key_file);
	return "";
}

// reloadIfInUse: the pair on disk served after a renewal.
std::string reload_if_in_use()
{
	auto conf = read_config();
	if (!conf) return "the renewed certificate could not be checked: cannot read the configuration";
	if (conf->proto != "https" || !in_use(*conf)) return "";
	auto served = tls().served_leaf ? tls().served_leaf() : std::nullopt;
	if (served) {
		auto pair = tls().pair_leaf ? tls().pair_leaf(cert_file, key_file) : std::nullopt;
		if (pair && !pair->empty() && !served->empty() && *pair == *served) return "";
	}
	if (auto e = load_served(cert_file, key_file); !e.empty()) return "the renewed certificate could not be put in use: " + e;
	log::info("renewed ACME certificate in use");
	return "";
}

// deactivate: back to the generated pair when the ACME one is in use.
std::string deactivate()
{
	auto conf = read_config();
	if (!conf) return "cannot read the configuration";
	if (!in_use(*conf)) return "";
	if (conf->proto == "https") {
		if (auto e = tls().ensure_generated ? tls().ensure_generated() : unwired; !e.empty()) return e;
		if (auto e = load_served(default_cert, default_key); !e.empty()) return e;
	}
	return update_tls(conf->proto, default_cert, default_key);
}

void remove_pair()
{
	for (const auto &path : {key_file, cert_file})
		if (int e = remove_file(path); e && e != ENOENT) log::error("failed to remove {}: {}", path, gopath::path_error("remove", path, e));
}

// nextRenewal: Le_NextRenewTime of the acme.sh domain configuration.
std::string next_renewal(const std::string &primary)
{
	if (primary.empty()) return "";
	auto data = read_file(data_dir() + "/" + primary + "_ecc/" + primary + ".conf");
	if (!data) return "";
	for (const auto &line : scan_lines(*data)) {
		if (line.rfind("Le_NextRenewTime=", 0) != 0) continue;
		std::string value = network::trim_chars(line.substr(17), "'\"");
		bool sign = !value.empty() && (value[0] == '+' || value[0] == '-');
		bool plain = value.size() > (sign ? 1u : 0u) && std::all_of(value.begin() + (sign ? 1 : 0), value.end(), digit);
		auto secs = plain ? network::atoi(value) : std::nullopt;
		if (secs && *secs > 0) return rfc3339(*secs);
	}
	return "";
}

// --- jobs -------------------------------------------------------------------------------------------------------

struct Job {
	std::string action, started_at, finished_at;
	bool ok = false, not_due = false;
	std::string error, output;
};

std::string job_json(const Job &j)
{
	return "{\"action\":" + q(j.action) + ",\"startedAt\":" + q(j.started_at) +
	       (j.finished_at.empty() ? "" : ",\"finishedAt\":" + q(j.finished_at)) + ",\"ok\":" + json_bool(j.ok) +
	       (j.not_due ? ",\"notDue\":true" : "") + (j.error.empty() ? "" : ",\"error\":" + q(j.error)) +
	       (j.output.empty() ? "" : ",\"output\":" + q(j.output)) + "}";
}

std::mutex job_mu;
std::condition_variable job_cv;
bool job_active = false;
std::optional<Job> last_job;

bool running()
{
	std::lock_guard lock(job_mu);
	return job_active;
}

std::optional<Job> last_run()
{
	std::lock_guard lock(job_mu);
	return last_job;
}

// tailBuffer: the last outputLimit bytes, "…" in front once cut.
struct TailBuffer {
	std::mutex mu;
	std::string buf;
	void write(std::string_view p)
	{
		std::lock_guard lock(mu);
		buf.append(p);
		if (buf.size() > output_limit) buf = "…" + buf.substr(buf.size() - output_limit);
	}
	std::string str()
	{
		std::lock_guard lock(mu);
		return buf;
	}
};

std::string scrub(std::string text, const std::vector<std::string> &secrets)
{
	for (const auto &s : secrets) {
		if (s.size() < 4) continue;
		std::string out;
		std::size_t i = 0;
		for (std::size_t j; (j = text.find(s, i)) != std::string::npos; i = j + s.size()) out += text.substr(i, j - i) + "***";
		text = out + text.substr(i);
	}
	return text;
}

std::vector<std::string> base_args()
{
	return {"--home", bin_dir(), "--config-home", data_dir(), "--log", data_dir() + "/" + log_name};
}

struct Step {
	std::vector<std::string> args;
	std::vector<int> ok_codes;
	int dns_sleep = 0;
};

// stepsTimeout: jobTimeout plus the DNS waits of the steps (an explicit --dnssleep or the wait a renewal reloads).
std::int64_t steps_timeout(const std::vector<Step> &steps)
{
	std::int64_t timeout = job_timeout;
	for (const auto &st : steps) {
		std::int64_t wait = st.dns_sleep;
		for (std::size_t i = 0; i + 1 < st.args.size(); i++)
			if (st.args[i] == "--dnssleep")
				if (auto seconds = network::atoi(st.args[i + 1]); seconds && *seconds > wait) wait = *seconds;
		if (wait > 0) timeout += std::min<std::int64_t>(wait, max_dns_sleep);
	}
	return timeout;
}

// ensurePrivateDirs: data/ and the TLS directory 0700, also when an earlier version made them wider.
std::string ensure_private_dirs()
{
	for (const auto &dir : {data_dir(), cert_dir()}) {
		if (auto e = gopath::mkdir_all(dir, 0700); !e.empty()) return e;
		if (::chmod(dir.c_str(), 0700) != 0) return gopath::path_error("chmod", dir, errno);
	}
	return "";
}

// command: the installed acme.sh run directly with an explicit environment, in data/, in a process group that
// ends with it.
Command acme_command(std::chrono::milliseconds timeout, const std::vector<std::string> &env, const std::vector<std::string> &args,
                     TailBuffer &out)
{
	Command c;
	c.argv = {script()};
	auto base = base_args();
	c.argv.insert(c.argv.end(), base.begin(), base.end());
	c.argv.insert(c.argv.end(), args.begin(), args.end());
	std::vector<std::string> e{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + data_dir(),
	                           "LE_WORKING_DIR=" + bin_dir(), "LE_CONFIG_HOME=" + data_dir()};
	e.insert(e.end(), env.begin(), env.end());
	c.env = e;
	c.dir = data_dir();
	c.timeout = timeout;
	c.group = true;
	c.kill_group_after = true;
	c.wait_delay = std::chrono::seconds(5);
	c.capture = Command::Capture::combined;
	c.sink = [&out](std::string_view data) { out.write(data); };
	return c;
}

std::string run_sync(const std::vector<std::string> &args, std::string &out)
{
	if (auto e = ensure_private_dirs(); !e.empty()) return e;
	TailBuffer buf;
	auto r = run(acme_command(std::chrono::minutes(2), {}, args, buf));
	out = buf.str();
	return r.error;
}

// retire: acme.sh forgets a domain record and its keys go.
void retire(const std::string &primary)
{
	if (primary.empty() || !domain_ok(primary)) return;
	std::string out;
	if (auto e = run_sync({"--remove", "-d", primary, "--ecc"}, out); !e.empty())
		log::warn("acme.sh --remove {}: {}: {}", primary, e, trim_space(out));
	if (auto e = remove_all(data_dir() + "/" + primary + "_ecc"); !e.empty())
		log::error("failed to remove the acme.sh record of {}: {}", primary, e);
}

void remove_log() { ::unlink((data_dir() + "/" + log_name).c_str()); }

Job run_steps(const Job &job, const std::vector<std::string> &env, const std::vector<std::string> &secrets, const std::vector<Step> &steps)
{
	std::int64_t timeout = steps_timeout(steps);
	auto deadline = std::chrono::steady_clock::now() + std::chrono::seconds(timeout);
	// The log holds the last run only: appending would grow the file on the SD card forever.
	remove_log();
	TailBuffer out;
	Job result = job;
	for (const auto &st : steps) {
		std::string error;
		int code = -1;
		if (error = ensure_private_dirs(); error.empty()) {
			auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now());
			auto r = run(acme_command(std::max(left, std::chrono::milliseconds(0)), env, st.args, out));
			bool expired = std::chrono::steady_clock::now() >= deadline;
			if (r.ok()) {
				code = 0;
			} else if (r.exited) {
				code = r.exit_code;
				if (std::find(st.ok_codes.begin(), st.ok_codes.end(), code) == st.ok_codes.end())
					error = "acme.sh exited with code " + std::to_string(code) + " (" + st.args[0] + ")";
			} else {
				error = r.error;
			}
			if (!error.empty() && expired) error = "acme.sh did not finish within " + go_duration(timeout);
		}
		if (error.empty()) {
			if (code == renew_skip) result.not_due = true;
			continue;
		}
		result.error = error;
		result.finished_at = now_rfc3339();
		result.output = scrub(out.str(), secrets);
		log::error("acme.sh {} failed: {}", job.action, result.error);
		return result;
	}
	result.ok = true;
	result.finished_at = now_rfc3339();
	result.output = scrub(out.str(), secrets);
	log::info("acme.sh {} finished", job.action);
	return result;
}

// startJob: the steps in the background, one job at a time, then after with the result.
std::string start_job(const std::string &action, std::vector<std::string> env, std::vector<std::string> secrets, std::vector<Step> steps,
                      std::function<std::string(const Job &)> after)
{
	Job job;
	{
		std::lock_guard lock(job_mu);
		if (job_active) return "an acme.sh operation is already running";
		job_active = true;
		job.action = action;
		job.started_at = now_rfc3339();
		last_job = job;
	}
	std::thread([job, env = std::move(env), secrets = std::move(secrets), steps = std::move(steps), after = std::move(after)] {
		Job result = run_steps(job, env, secrets, steps);
		// The follow-up (serving the new pair) belongs to the job.
		if (after)
			if (auto e = after(result); !e.empty()) {
				log::error("acme.sh {}: {}", job.action, e);
				result.ok = false;
				result.not_due = false;
				result.error = e;
			}
		std::lock_guard lock(job_mu);
		last_job = result;
		job_active = false;
		job_cv.notify_all();
	}).detach();
	return "";
}

Step renew_step(const Settings &s, bool force)
{
	Step st{{"--renew", "-d", s.primary(), "--ecc"}, {}, static_cast<int>(s.dns_sleep)};
	if (force) st.args.push_back("--force");
	else st.ok_codes = {renew_skip};
	return st;
}

void renew_due()
{
	std::unique_lock lock(op_mu, std::try_to_lock);
	if (!lock.owns_lock() || !installed()) return;
	auto settings = load_settings();
	if (!settings) return;
	auto e = start_job("cron", {}, {}, {renew_step(*settings, false)}, [](const Job &j) -> std::string {
		if (!j.ok || j.not_due) return "";
		return reload_if_in_use();
	});
	if (!e.empty()) log::debug("acme.sh renewal skipped: {}", e);
}

// --- release and install ----------------------------------------------------------------------------------------

struct Release {
	std::string tag, checked_at;
};
std::mutex latest_mu;
std::optional<Release> latest_known;

std::optional<Release> known_latest()
{
	std::lock_guard lock(latest_mu);
	return latest_known;
}

std::optional<Release> latest_release(std::string &error)
{
	http_client::Request req;
	req.url = check::latest_url;
	req.headers = {{"Accept", "application/vnd.github+json"}, {"User-Agent", "NanoKVM-OS"}};
	req.timeout = std::chrono::seconds(30);
	std::string body;
	req.sink = [&body](std::string_view data) {
		if (body.size() < (1u << 20)) body.append(data.substr(0, (1u << 20) - body.size()));
		return true;
	};
	auto rsp = http_client::perform(req);
	if (rsp.status == 0) {
		error = "GitHub is not reachable";
		return std::nullopt;
	}
	if (rsp.status != 200) {
		error = "GitHub answered " + std::to_string(rsp.status);
		return std::nullopt;
	}
	std::string decode_error;
	auto doc = godecode::decode_first(body, decode_error);
	std::string tag_name;
	bool draft = false, prerelease = false, good = doc && (doc->kind == K::object || doc->kind == K::null);
	if (doc && doc->kind == K::object)
		for (const auto &[k, v] : doc->members) {
			if (godecode::key_matches(k, "tag_name")) good = godecode::get(v, tag_name) && good;
			else if (godecode::key_matches(k, "draft")) good = godecode::get(v, draft) && good;
			else if (godecode::key_matches(k, "prerelease")) good = godecode::get(v, prerelease) && good;
		}
	if (!good) {
		error = "unexpected answer from GitHub";
		return std::nullopt;
	}
	std::string tag = trim_space(tag_name);
	if (!tag.empty() && tag[0] == 'v') tag.erase(0, 1);
	if (draft || prerelease || !release_tag(tag)) {
		error = "unexpected release " + go_quote(tag_name);
		return std::nullopt;
	}
	Release r{tag, now_rfc3339()};
	std::lock_guard lock(latest_mu);
	latest_known = r;
	return r;
}

// utils.Download: the response (200, an archive content type, at most max bytes) written to a new file.
bool download(const std::string &url, const std::string &target, std::size_t max)
{
	if (!gopath::mkdir_all(gopath::dir(target), 0755).empty()) return false;
	int fd = -1;
	bool failed = false;
	std::size_t written = 0;
	http_client::Request req;
	req.url = url;
	req.timeout = std::chrono::minutes(15);
	req.on_headers = [&](int status, const std::vector<std::pair<std::string, std::string>> &headers) {
		if (status != 200) return false;
		std::string type, length;
		for (const auto &[name, value] : headers) {
			if (name == "content-type") type = value;
			else if (name == "content-length") length = value;
		}
		std::string media = to_lower_ascii(trim_space(type.substr(0, type.find(';'))));
		static const std::set<std::string> allowed{"application/octet-stream", "application/gzip", "application/x-gzip",
		                                           "application/x-compressed", "application/zip"};
		if (!allowed.count(media)) return false;
		if (!length.empty())
			if (auto n = network::parse_uint(trim_space(length), 10, 64); n && *n > max) return false;
		fd = ::open(target.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600);
		return fd >= 0;
	};
	req.sink = [&](std::string_view data) {
		if (fd < 0 || written + data.size() > max) {
			failed = true;
			return false;
		}
		for (std::size_t done = 0; done < data.size();) {
			ssize_t n = ::write(fd, data.data() + done, data.size() - done);
			if (n < 0 && errno == EINTR) continue;
			if (n <= 0) {
				failed = true;
				return false;
			}
			done += static_cast<std::size_t>(n);
		}
		written += data.size();
		return true;
	};
	auto rsp = http_client::perform(req);
	bool ok = fd >= 0 && !failed && rsp.status == 200 && rsp.error.empty();
	if (fd >= 0 && ::close(fd) != 0) ok = false;
	if (!ok && fd >= 0) ::unlink(target.c_str());
	return ok;
}

// A gzip stream read whole into memory (at most limit bytes of output).
bool gunzip(const std::string &path, std::string &out, std::string &error)
{
	auto data = read_file(path);
	if (!data) {
		error = "open " + path + ": " + network::errno_text(errno);
		return false;
	}
	z_stream z{};
	if (inflateInit2(&z, 16 + MAX_WBITS) != Z_OK) {
		error = "decompress: invalid header";
		return false;
	}
	z.next_in = reinterpret_cast<Bytef *>(data->data());
	z.avail_in = static_cast<uInt>(data->size());
	char buf[65536];
	int rc = Z_OK;
	while (rc != Z_STREAM_END) {
		z.next_out = reinterpret_cast<Bytef *>(buf);
		z.avail_out = sizeof buf;
		rc = inflate(&z, Z_NO_FLUSH);
		if (rc != Z_OK && rc != Z_STREAM_END) {
			inflateEnd(&z);
			error = rc == Z_DATA_ERROR && z.total_out == 0 && z.total_in < 10 ? "decompress: invalid header" : "decompress: corrupt data";
			if (rc == Z_BUF_ERROR) error = "unexpected EOF";
			return false;
		}
		out.append(buf, sizeof buf - z.avail_out);
		if (out.size() > (64u << 20)) {
			inflateEnd(&z);
			error = "decompress: corrupt data";
			return false;
		}
	}
	inflateEnd(&z);
	return true;
}

struct TarEntry {
	std::string name;
	char type = 0;
	std::int64_t size = 0;
	std::string_view data;
};

// archive/tar's Reader.Next over a whole archive: USTAR/PAX/GNU headers, PAX path and size records, GNU long names,
// global headers returned as entries.
bool tar_entries(std::string_view tar, std::vector<TarEntry> &entries, std::string &error)
{
	auto field = [](std::string_view block, std::size_t off, std::size_t len) {
		auto f = block.substr(off, len);
		auto nul = f.find('\0');
		return std::string(nul == std::string_view::npos ? f : f.substr(0, nul));
	};
	auto number = [](std::string_view f, std::int64_t &out) {
		if (!f.empty() && (static_cast<unsigned char>(f[0]) & 0x80)) {
			std::int64_t v = static_cast<unsigned char>(f[0]) & 0x7f;
			for (std::size_t i = 1; i < f.size(); i++) v = (v << 8) | static_cast<unsigned char>(f[i]);
			out = v;
			return true;
		}
		std::string s(f);
		auto b = s.find_first_not_of(" \0", 0, 2);
		auto e = s.find_last_not_of(" \0", std::string::npos, 2);
		if (b == std::string::npos) {
			out = 0;
			return true;
		}
		s = s.substr(b, e - b + 1);
		std::int64_t v = 0;
		for (char c : s) {
			if (c < '0' || c > '7') return false;
			v = v * 8 + (c - '0');
		}
		out = v;
		return true;
	};
	std::size_t pos = 0;
	std::map<std::string, std::string> pax;
	std::string long_name;
	while (true) {
		if (pos + 512 > tar.size()) {
			if (pos == tar.size()) return true;  // io.EOF
			error = "unexpected EOF";
			return false;
		}
		auto block = tar.substr(pos, 512);
		if (block.find_first_not_of('\0') == std::string_view::npos) {
			if (pos + 1024 > tar.size()) return true;
			auto next = tar.substr(pos + 512, 512);
			if (next.find_first_not_of('\0') == std::string_view::npos) return true;
			error = "archive/tar: invalid tar header";
			return false;
		}
		std::int64_t sum = 0, recorded = 0;
		for (std::size_t i = 0; i < 512; i++) sum += (i >= 148 && i < 156) ? ' ' : static_cast<unsigned char>(block[i]);
		std::int64_t signed_sum = 0;
		for (std::size_t i = 0; i < 512; i++) signed_sum += (i >= 148 && i < 156) ? ' ' : static_cast<signed char>(block[i]);
		if (!number(block.substr(148, 8), recorded) || (recorded != sum && recorded != signed_sum)) {
			error = "archive/tar: invalid tar header";
			return false;
		}
		TarEntry e;
		e.type = block[156];
		e.name = field(block, 0, 100);
		bool ustar = block.substr(257, 6) == std::string_view("ustar\0", 6) && block.substr(263, 2) == "00";
		if (ustar)
			if (auto prefix = field(block, 345, 155); !prefix.empty()) e.name = prefix + "/" + e.name;
		if (!number(block.substr(124, 12), e.size) || e.size < 0) {
			error = "archive/tar: invalid tar header";
			return false;
		}
		if (auto it = pax.find("size"); it != pax.end())
			if (auto n = network::atoi(it->second)) e.size = *n;
		std::size_t data_at = pos + 512;
		std::size_t padded = static_cast<std::size_t>((e.size + 511) / 512 * 512);
		if (data_at + static_cast<std::size_t>(e.size) > tar.size()) {
			error = "unexpected EOF";
			return false;
		}
		e.data = tar.substr(data_at, static_cast<std::size_t>(e.size));
		pos = std::min(tar.size(), data_at + padded);
		if (e.type == 'x' || e.type == 'g') {
			std::map<std::string, std::string> records;
			std::string_view d = e.data;
			while (!d.empty()) {
				auto sp = d.find(' ');
				if (sp == std::string_view::npos) break;
				auto len = network::atoi(std::string(d.substr(0, sp)));
				if (!len || *len <= 0 || static_cast<std::size_t>(*len) > d.size()) {
					error = "archive/tar: invalid tar header";
					return false;
				}
				auto record = d.substr(sp + 1, static_cast<std::size_t>(*len) - sp - 1);
				if (!record.empty() && record.back() == '\n') record.remove_suffix(1);
				auto eq = record.find('=');
				if (eq != std::string_view::npos) records[std::string(record.substr(0, eq))] = std::string(record.substr(eq + 1));
				d.remove_prefix(static_cast<std::size_t>(*len));
			}
			if (e.type == 'g') {
				if (auto it = records.find("path"); it != records.end()) e.name = it->second;
				entries.push_back(e);
			} else {
				pax = records;
			}
			continue;
		}
		if (e.type == 'L') {
			long_name = field(e.data, 0, e.data.size());
			continue;
		}
		if (e.type == 'K') continue;
		if (!long_name.empty()) e.name = long_name;
		if (auto it = pax.find("path"); it != pax.end()) e.name = it->second;
		long_name.clear();
		pax.clear();
		if (e.type == '\0') e.type = !e.name.empty() && e.name.back() == '/' ? '5' : '0';
		entries.push_back(e);
	}
}

// extract: the script (0755) and the DNS hooks (0644) of the archive under dest, the archive prefix removed.
std::string extract(const std::string &tarball, const std::string &dest, const std::string &tag)
{
	std::string data, error;
	if (!gunzip(tarball, data, error)) return error;
	if (auto e = gopath::mkdir_all(dest + "/dnsapi", 0755); !e.empty()) return e;
	std::vector<TarEntry> entries;
	bool complete = tar_entries(data, entries, error);
	std::string prefix = "acme.sh-" + tag + "/";
	int hooks = 0;
	bool has_script = false;
	for (const auto &e : entries) {
		// ^acme\.sh-<tag>/(acme\.sh|dnsapi/dns_[a-z0-9_]+\.sh)$
		if (e.name.rfind(prefix, 0) != 0) continue;
		std::string rel = e.name.substr(prefix.size());
		bool hook = rel.size() > 14 && rel.rfind("dnsapi/dns_", 0) == 0 && rel.compare(rel.size() - 3, 3, ".sh") == 0 &&
		            std::all_of(rel.begin() + 11, rel.end() - 3, [](char c) { return lower_alnum(c) || c == '_'; });
		if (rel != "acme.sh" && !hook) continue;
		if (e.type != '0') return e.name + " is not a regular file";
		if (e.size > max_file) return e.name + " is larger than " + std::to_string(max_file) + " bytes";
		mode_t mode = 0644;
		if (rel == "acme.sh") {
			mode = 0755;
			has_script = true;
		} else {
			hooks++;
		}
		std::string path = dest + "/" + rel;
		int fd = ::open(path.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, mode);
		if (fd < 0) return gopath::path_error("open", path, errno);
		bool good = true;
		for (std::size_t done = 0; done < e.data.size();) {
			ssize_t n = ::write(fd, e.data.data() + done, e.data.size() - done);
			if (n < 0 && errno == EINTR) continue;
			if (n <= 0) {
				good = false;
				break;
			}
			done += static_cast<std::size_t>(n);
		}
		int ce = ::close(fd);
		if (!good) return gopath::path_error("write", path, errno);
		if (ce != 0) return gopath::path_error("close", path, errno);
	}
	if (!complete) return error;
	if (!has_script || hooks == 0) return "the archive holds no acme.sh script or no DNS hooks";
	return "";
}

std::string install_tag(const std::string &tag, const std::string &url)
{
	if (auto e = gopath::mkdir_all(root, 0700); !e.empty()) return e;
	std::string tarball = root + "/download.tar.gz";
	::unlink(tarball.c_str());
	if (!download(url, tarball, max_tarball)) {
		log::error("download acme.sh {} from {}: failed", tag, url);
		return "download acme.sh " + tag + ": GitHub is not reachable";
	}
	struct Remove {
		std::string path;
		~Remove() { ::unlink(path.c_str()); }
	} remove_tarball{tarball};
	std::string staging = root + "/bin.staging";
	(void)remove_all(staging);
	if (auto e = extract(tarball, staging, tag); !e.empty()) {
		(void)remove_all(staging);
		return "unpack acme.sh " + tag + ": " + e;
	}
	int err = 0;
	auto data = gopath::read_file(staging + "/acme.sh", err);
	if (!data) {
		(void)remove_all(staging);
		return gopath::path_error("open", staging + "/acme.sh", err);
	}
	if (auto got = script_version(*data); got != tag) {
		(void)remove_all(staging);
		return "the downloaded script says version " + go_quote(got) + ", the release is " + tag;
	}
	std::string old = root + "/bin.old";
	(void)remove_all(old);
	if (stat_ok(bin_dir()) && ::rename(bin_dir().c_str(), old.c_str()) != 0) {
		std::string e = "rename " + bin_dir() + " " + old + ": " + network::errno_text(errno);
		(void)remove_all(staging);
		return e;
	}
	if (::rename(staging.c_str(), bin_dir().c_str()) != 0) {
		std::string e = "rename " + staging + " " + bin_dir() + ": " + network::errno_text(errno);
		::rename(old.c_str(), bin_dir().c_str());
		(void)remove_all(staging);
		return e;
	}
	(void)remove_all(old);
	if (auto e = gopath::mkdir_all(data_dir(), 0700); !e.empty()) return e;
	log::info("acme.sh {} installed in {}", tag, bin_dir());
	return "";
}

// --- status -----------------------------------------------------------------------------------------------------

std::string status()
{
	bool is_installed = installed();
	auto latest = known_latest();
	bool is_running = running();
	auto last = last_run();
	std::string version;
	bool update = false;
	if (is_installed) {
		version = installed_version();
		update = latest && newer_version(version, latest->tag);
	}
	auto settings = load_settings();
	std::string next = settings ? next_renewal(settings->primary()) : "";
	std::optional<std::string> certificate = tls().describe ? tls().describe(cert_file, key_file) : std::nullopt;
	bool used = false;
	if (auto conf = read_config()) used = in_use(*conf);
	std::string out = "{\"installed\":" + json_bool(is_installed);
	if (!version.empty()) out += ",\"version\":" + q(version);
	if (latest) out += ",\"latest\":{\"tag\":" + q(latest->tag) + ",\"checkedAt\":" + q(latest->checked_at) + "}";
	out += ",\"updateAvailable\":" + json_bool(update);
	if (settings) out += ",\"settings\":" + settings_json(*settings, false);
	if (certificate) out += ",\"certificate\":" + *certificate;
	out += ",\"inUse\":" + json_bool(used);
	if (!next.empty()) out += ",\"nextRenewal\":" + q(next);
	out += ",\"running\":" + json_bool(is_running);
	if (last) out += ",\"lastRun\":" + job_json(*last);
	return out + "}";
}

// The issue request: the settings, the provider's variables and the external account binding.
struct IssueReq {
	Settings settings;
	std::optional<std::map<std::string, std::string>> dns_env;
	std::string eab_kid, eab_hmac_key;
};

bool bind_issue(const std::string &body, IssueReq &req)
{
	std::string error;
	auto doc = godecode::decode_first(body, error);
	if (!doc) return false;
	if (doc->kind == K::null) return true;
	if (doc->kind != K::object) return false;
	bool good = true, known = false;
	for (const auto &[k, v] : doc->members) {
		good = settings_member(req.settings, k, v, known) && good;
		if (known) continue;
		if (godecode::key_matches(k, "dnsEnv")) {
			if (v.kind == K::null) {
				req.dns_env.reset();
			} else if (v.kind != K::object) {
				good = false;
			} else {
				if (!req.dns_env) req.dns_env.emplace();
				for (const auto &[name, value] : v.members) {
					std::string text;
					good = godecode::get(value, text) && good;
					(*req.dns_env)[name] = text;
				}
			}
		} else if (godecode::key_matches(k, "eabKid")) {
			good = godecode::get(v, req.eab_kid) && good;
		} else if (godecode::key_matches(k, "eabHmacKey")) {
			good = godecode::get(v, req.eab_hmac_key) && good;
		}
	}
	return good;
}

// checkEnv: only the variables the hook declares, with plain values.
std::string check_env(const Provider &p, const std::map<std::string, std::string> &env, std::vector<std::string> &vars,
                      std::vector<std::string> &secrets)
{
	std::set<std::string> allowed;
	for (const auto &o : p.options) allowed.insert(o.name);
	for (const auto &o : p.options_alt) allowed.insert(o.name);
	for (const auto &[name, raw] : env) {
		if (!allowed.count(name)) return name + " is not a setting of " + p.hook;
		std::string value = trim_space(raw);
		if (value.empty()) continue;
		if (value.size() > 1024 || value.find_first_of(std::string_view("\0\r\n", 3)) != std::string::npos)
			return "the value of " + name + " is not usable";
		vars.push_back(name + "=" + value);
		secrets.push_back(value);
	}
	if (vars.empty() && !p.options.empty()) return p.name + " needs its credentials";
	return "";
}

// issueArgs: the acme.sh --issue command line (--force: the button is an explicit request).
std::string issue_args(const Settings &s, const IssueReq &req, std::vector<std::string> &args, std::vector<std::string> &env,
                       std::vector<std::string> &secrets)
{
	args = {"--issue", "--server", ca_servers.at(s.ca), "--keylength", "ec-256", "--force"};
	for (const auto &d : *s.domains) args.insert(args.end(), {"-d", d});
	if (!s.email.empty()) args.insert(args.end(), {"--accountemail", s.email});
	if (s.challenge == "http") {
		args.insert(args.end(), {"--webroot", challenge_root});
	} else if (s.challenge == "dns") {
		auto p = provider(s.dns_provider);
		if (!p) return "the DNS provider is not installed";
		if (auto e = check_env(*p, req.dns_env ? *req.dns_env : std::map<std::string, std::string>{}, env, secrets); !e.empty())
			return e;
		args.insert(args.end(), {"--dns", s.dns_provider});
		if (s.dns_sleep > 0) args.insert(args.end(), {"--dnssleep", std::to_string(s.dns_sleep)});
	}
	std::string kid = trim_space(req.eab_kid), hmac = trim_space(req.eab_hmac_key);
	if ((s.ca == "google" || s.ca == "google_test") && (kid.empty() || hmac.empty())) return "Google needs the EAB key ID and HMAC key";
	if (!kid.empty() || !hmac.empty()) {
		if (kid.empty() || hmac.empty() || kid.size() > 256 || hmac.size() > 1024 ||
		    (kid + hmac).find_first_of(std::string_view(" \t\r\n\0", 5)) != std::string::npos)
			return "the EAB key ID and HMAC key are not usable";
		args.insert(args.end(), {"--eab-kid", kid, "--eab-hmac-key", hmac});
		secrets.push_back(hmac);
	}
	return "";
}

std::vector<std::string> install_cert_args(const std::string &primary)
{
	return {"--install-cert", "-d", primary, "--ecc", "--fullchain-file", cert_file, "--key-file", key_file};
}

// read_config's default: config.Read's proto and cert (the TLS settings' reader) and port.http.
std::optional<TlsConfig> read_server_config()
{
	auto settings = config::read_tls();
	if (!settings) return std::nullopt;
	TlsConfig c;
	c.proto = settings->proto;
	c.crt = settings->cert.crt;
	c.key = settings->cert.key;
	std::string error;
	if (auto text = read_file(config::configuration_file))
		if (auto values = yaml::parse(*text, error)) c.http_port = static_cast<int>(yaml::as_int(values->path("port.http")).value_or(0));
	return c;
}

} // namespace

Tls &tls()
{
	static Tls t = [] {
		namespace st = system::tls;
		Tls d;
		d.read_config = read_server_config;
		d.update_tls = [](const std::string &proto, const std::string &crt, const std::string &key) {
			config::Cert cert;
			cert.crt = crt;
			cert.key = key;
			std::string error;
			if (!config::update_tls(proto, cert, error) && error.empty()) error = "cannot update the configuration";
			return error;
		};
		d.load = [](const std::string &crt, const std::string &key) { return st::server_certificate().load(crt, key); };
		d.served_leaf = []() -> std::optional<std::string> {
			auto served = st::server_certificate().current();
			if (!served.pair || served.pair->pair.chain.empty()) return std::nullopt;
			return served.pair->pair.chain[0];
		};
		d.pair_leaf = [](const std::string &crt, const std::string &key) -> std::optional<std::string> {
			auto pair = st::load_certificate_pair(crt, key);
			if (!pair || pair->pair.chain.empty()) return std::nullopt;
			return pair->pair.chain[0];
		};
		d.ensure_generated = [] { return st::ensure_server_certificate(default_cert, default_key, st::Paths{}, st::now()); };
		d.describe = [](const std::string &crt, const std::string &key) -> std::optional<std::string> {
			auto pair = st::load_certificate_pair(crt, key);
			if (!pair) return std::nullopt;
			return st::describe(pair->pair, st::now());
		};
		return d;
	}();
	return t;
}

void challenge(const http::Request &req, http::Response &res)
{
	auto not_found = [&] {
		res.set_header("X-Content-Type-Options", "nosniff");
		res.text(404, "404 page not found\n");
	};
	if (req.method != "GET" && req.method != "HEAD") {
		res.set_header("X-Content-Type-Options", "nosniff");
		res.text(405, "method not allowed\n");
		return;
	}
	std::string prefix = challenge_prefix;
	if (req.path.rfind(prefix, 0) != 0 || !token_ok(req.path.substr(prefix.size()))) return not_found();
	std::string path = challenge_root + "/.well-known/acme-challenge/" + req.path.substr(prefix.size());
	struct stat st{};
	if (::stat(path.c_str(), &st) != 0 || !S_ISREG(st.st_mode)) return not_found();
	auto data = read_file(path);
	if (!data) return not_found();
	res.set_header("Cache-Control", "no-store");
	res.text(200, *data, "text/plain; charset=utf-8");
}

Reply get_status() { return ok(status()); }

Reply get_providers()
{
	if (!installed()) return fail(-1, "acme.sh is not installed");
	std::vector<Provider> providers;
	if (!list_providers(hooks_dir(), providers)) return fail(-2, "cannot read the DNS hooks");
	std::string out = "[";
	for (std::size_t i = 0; i < providers.size(); i++) out += (i ? "," : "") + provider_json(providers[i]);
	return ok(out + "]");
}

Reply install()
{
	std::string e;
	{
		std::lock_guard lock(op_mu);
		if (running()) {
			e = "an acme.sh operation is running";
		} else if (auto release = latest_release(e)) {
			e = install_tag(release->tag, check::tarball_base + release->tag + ".tar.gz");
		} else {
			e = "find the latest acme.sh release: " + e;
		}
	}
	if (!e.empty()) {
		log::error("acme.sh install failed: {}", e);
		return fail(-2, e);
	}
	return ok(status());
}

Reply check_update()
{
	std::string e;
	if (!latest_release(e)) return fail(-2, e);
	return ok(status());
}

Reply uninstall()
{
	std::lock_guard lock(op_mu);
	if (running()) return fail(-1, "an acme.sh operation is running");
	if (auto e = deactivate(); !e.empty()) {
		log::error("failed to return to the generated certificate: {}", e);
		return fail(-2, "cannot return to the generated certificate");
	}
	remove_pair();
	if (auto e = remove_all(root); !e.empty()) {
		log::error("failed to remove {}: {}", root, e);
		return fail(-2, "cannot remove the acme.sh directory");
	}
	{
		std::lock_guard l(job_mu);
		last_job.reset();
	}
	log::info("acme.sh removed");
	return ok(status());
}

Reply issue(const std::string &body)
{
	IssueReq req;
	if (!bind_issue(body, req)) return fail(-1, "invalid arguments");
	if (!installed()) return fail(-1, "acme.sh is not installed");
	Settings settings = req.settings;
	settings.issued_at.clear();
	if (auto e = normalize(settings); !e.empty()) return fail(-1, e);
	std::vector<std::string> args, env, secrets;
	if (auto e = issue_args(settings, req, args, env, secrets); !e.empty()) return fail(-1, e);
	if (settings.challenge == "http") {
		// The CA fetches the token on port 80 only.
		auto conf = read_config();
		if (!conf) return fail(-2, "cannot read the configuration");
		if (conf->http_port != 80)
			return fail(-1, "HTTP validation needs the plain-HTTP port to be 80 (it is " + std::to_string(conf->http_port) + ")");
	}
	std::lock_guard lock(op_mu);
	if (settings.challenge == "http")
		if (auto e = gopath::mkdir_all(challenge_root + "/.well-known/acme-challenge", 0755); !e.empty())
			log::error("failed to create the ACME challenge directory: {}", e);
	if (!gopath::mkdir_all(cert_dir(), 0700).empty()) return fail(-2, "cannot prepare the certificate directory");
	// The certificate in place keeps its settings (they name the record renewals act on) until the new one is in.
	std::string previous;
	if (auto saved = load_settings()) previous = saved->primary();
	std::vector<Step> steps{{args, {}, 0}, {install_cert_args(settings.primary()), {}, 0}};
	auto e = start_job("issue", env, secrets, steps, [settings, previous](const Job &j) mutable -> std::string {
		if (!j.ok) {
			if (settings.primary() != previous) retire(settings.primary());
			return "";
		}
		// --install-cert has written the new pair: from here on the new record owns acme.crt/acme.key.
		settings.issued_at = now_rfc3339();
		if (auto err = save_settings(settings); !err.empty())
			return "the certificate was issued but its settings could not be saved: " + err;
		if (!previous.empty() && previous != settings.primary()) retire(previous);
		if (auto err = activate(); !err.empty()) return "the certificate was issued but could not be put in use: " + err;
		return "";
	});
	if (!e.empty()) return fail(-1, e);
	return ok(status());
}

Reply renew()
{
	auto settings = load_settings();
	if (!settings || !installed()) return fail(-1, "no certificate has been issued");
	std::lock_guard lock(op_mu);
	auto e = start_job("renew", {}, {}, {renew_step(*settings, true)}, [](const Job &j) -> std::string {
		if (!j.ok) return "";
		return reload_if_in_use();
	});
	if (!e.empty()) return fail(-1, e);
	return ok(status());
}

Reply remove()
{
	std::lock_guard lock(op_mu);
	if (running()) return fail(-1, "an acme.sh operation is running");
	if (auto e = deactivate(); !e.empty()) {
		log::error("failed to return to the generated certificate: {}", e);
		return fail(-2, "cannot return to the generated certificate");
	}
	if (auto settings = load_settings(); settings && installed()) retire(settings->primary());
	remove_pair();
	if (int e = remove_file(settings_file()); e && e != ENOENT)
		log::error("failed to remove the ACME settings: {}", gopath::path_error("remove", settings_file(), e));
	remove_log();
	{
		std::lock_guard l(job_mu);
		last_job.reset();
	}
	return ok(status());
}

void start()
{
	std::thread([] {
		std::this_thread::sleep_for(std::chrono::minutes(10));
		while (true) {
			renew_due();
			std::this_thread::sleep_for(std::chrono::hours(12));
		}
	}).detach();
}

namespace check {

void renew_due() { acme::renew_due(); }

void wait_job()
{
	std::unique_lock lock(job_mu);
	job_cv.wait(lock, [] { return !job_active; });
}

std::string script_version(const std::string &script) { return acme::script_version(script); }
bool newer_version(const std::string &installed, const std::string &candidate) { return acme::newer_version(installed, candidate); }

std::string providers_json(const std::string &dir)
{
	std::vector<Provider> providers;
	if (!list_providers(dir, providers)) return "ERR";
	std::string out = "[";
	for (std::size_t i = 0; i < providers.size(); i++) out += (i ? "," : "") + provider_json(providers[i]);
	return out + "]";
}

std::string provider_json(const std::string &hook, const std::string &script)
{
	return acme::provider_json(parse_provider_info(hook, script));
}

std::string normalize(const std::string &settings_json_text, std::string &error)
{
	IssueReq req;
	if (!bind_issue(settings_json_text, req)) {
		error = "invalid arguments";
		return "";
	}
	error = acme::normalize(req.settings);
	return error.empty() ? settings_json(req.settings, false) : "";
}

std::string issue_args(const std::string &request_json, std::string &error)
{
	IssueReq req;
	if (!bind_issue(request_json, req)) {
		error = "invalid arguments";
		return "";
	}
	Settings s = req.settings;
	if (error = acme::normalize(s); !error.empty()) return "";
	std::vector<std::string> args, env, secrets;
	if (error = acme::issue_args(s, req, args, env, secrets); !error.empty()) return "";
	std::sort(env.begin(), env.end());
	std::sort(secrets.begin(), secrets.end());
	return network::join(args, " ") + "\n" + network::join(env, " ") + "\n" + network::join(secrets, " ");
}

std::string scrub(const std::string &text, const std::vector<std::string> &secrets) { return acme::scrub(text, secrets); }

std::string tail_buffer(const std::vector<std::string> &writes)
{
	TailBuffer b;
	for (const auto &w : writes) b.write(w);
	return b.str();
}

std::int64_t steps_timeout(const std::vector<std::vector<std::string>> &steps, int dns_sleep)
{
	std::vector<Step> list;
	for (const auto &args : steps) list.push_back({args, {}, dns_sleep});
	return acme::steps_timeout(list);
}

} // namespace check

} // namespace nkos::extensions::acme
