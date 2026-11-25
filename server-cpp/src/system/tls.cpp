#include "system/tls.hpp"

#include "api/multipart.hpp"
#include "api/request.hpp"
#include "auth/session.hpp"
#include "common/gojson.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/process.hpp"
#include "http/server.hpp"

#include <openssl/ssl.h>

#include <cerrno>
#include <chrono>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <future>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

namespace nkos::system::tls {

namespace {

// A certificate chain and a key fit in a few KiB.
constexpr size_t max_certificate_upload = 256 * 1024;

std::string dir_of(const std::string &path)
{
	auto slash = path.rfind('/');
	if (slash == std::string::npos) return ".";
	return slash == 0 ? "/" : path.substr(0, slash);
}
std::string base_of(const std::string &path) { return path.substr(path.rfind('/') + 1); }

// atomicfile.SyncDir.
std::string sync_dir(const std::string &dir)
{
	int fd = ::open(dir.c_str(), O_RDONLY | O_DIRECTORY | O_CLOEXEC);
	if (fd < 0) return path_error("open", dir, errno);
	std::string err;
	if (::fsync(fd) != 0) err = path_error("sync", dir, errno);
	::close(fd);
	return err;
}

// os.MkdirAll.
std::string mkdir_all(const std::string &dir, mode_t mode)
{
	struct stat st{};
	if (::stat(dir.c_str(), &st) == 0) return S_ISDIR(st.st_mode) ? "" : path_error("mkdir", dir, ENOTDIR);
	std::string parent = dir_of(dir);
	if (parent != dir && parent != "/" && parent != ".")
		if (auto e = mkdir_all(parent, mode); !e.empty()) return e;
	if (::mkdir(dir.c_str(), mode) != 0 && errno != EEXIST) return path_error("mkdir", dir, errno);
	return "";
}

bool after(Instant t, std::int64_t unix) { return t.sec > unix || (t.sec == unix && t.nsec > 0); }

std::string ensure_generated(const std::string &crt, const std::string &key, Instant at)
{
	if (auto pair = load_x509_key_pair(crt, key)) {
		// A pair made while the clock still stood in 1970 expired long ago: browsers refuse it with a date error.
		if (!after(at, pair->leaf.not_after)) return "";
		log::warn("the generated HTTPS certificate expired on {}; generating a new one", format_date(pair->leaf.not_after));
		// Without the old certificate, a power cut while the new pair is written leaves the state of an
		// interrupted first boot, repaired at the next start.
		if (::unlink(crt.c_str()) != 0) return path_error("remove", crt, errno);
		if (auto e = sync_dir(dir_of(crt)); !e.empty()) return e;
		return generate_certificate_files(crt, key, at);
	}
	// Only a complete pair counts as existing: generation publishes the key first, so a lone key is an
	// interrupted first boot and is regenerated.
	struct stat st{};
	bool crt_missing = ::stat(crt.c_str(), &st) != 0 && errno == ENOENT;
	bool key_missing = ::stat(key.c_str(), &st) != 0 && errno == ENOENT;
	if (!crt_missing && !key_missing) return "existing TLS certificate cannot be loaded";
	if (auto e = mkdir_all(dir_of(crt), 0755); !e.empty()) return e;
	return generate_certificate_files(crt, key, at);
}

// prepareCustomTLSFile: a complete, synced replacement beside the target.
std::string stage_file(const std::string &path, const std::string &data, mode_t mode, std::string &name)
{
	name = dir_of(path) + "/." + base_of(path) + ".pending-XXXXXX";
	int fd = ::mkstemp(name.data());
	if (fd < 0) {
		std::string e = path_error("open", name, errno);
		name.clear();
		return e;
	}
	std::string err;
	if (::fchmod(fd, mode) != 0) err = path_error("chmod", name, errno);
	for (size_t done = 0; err.empty() && done < data.size();) {
		ssize_t n = ::write(fd, data.data() + done, data.size() - done);
		if (n < 0 && errno == EINTR) continue;
		if (n <= 0) err = path_error("write", name, errno);
		else done += static_cast<size_t>(n);
	}
	if (err.empty() && ::fsync(fd) != 0) err = path_error("sync", name, errno);
	if (::close(fd) != 0 && err.empty()) err = path_error("close", name, errno);
	if (!err.empty()) {
		::unlink(name.c_str());
		name.clear();
	}
	return err;
}

// storeCustomPair: both files staged and synced before either is replaced, so a full file system never leaves an
// old certificate with a new key. restore puts the previous files back when a later step fails.
std::string store_custom_pair(const Paths &paths, const std::string &cert_pem, const std::string &key_pem,
                              std::function<std::string()> &restore)
{
	if (auto e = mkdir_all(dir_of(paths.custom_key), 0700); !e.empty()) return e;
	struct File {
		std::string path, data;
		mode_t mode;
		std::string staged, previous;
		bool existed = false;
	};
	auto files = std::make_shared<std::vector<File>>(std::vector<File>{{paths.custom_key, key_pem, 0600}, {paths.custom_crt, cert_pem, 0644}});
	auto cleanup = [files] {
		for (auto &f : *files)
			if (!f.staged.empty()) ::unlink(f.staged.c_str());
	};
	for (auto &f : *files) {
		std::string error;
		if (auto old = read_file(f.path, error)) {
			f.previous = std::move(*old);
			f.existed = true;
		} else if (errno != ENOENT) {
			return "read previous TLS file " + f.path + ": " + error;
		}
	}
	auto restore_up_to = [files](size_t last) {
		std::string errs;
		for (size_t j = last + 1; j-- > 0;) {
			const File &old = (*files)[j];
			std::string e;
			if (old.existed) {
				if (!config::atomic_write(old.path, old.previous, old.mode)) e = errno_text(errno);
			} else if (::unlink(old.path.c_str()) != 0 && errno != ENOENT) {
				e = errno_text(errno);
			}
			if (!e.empty()) errs += (errs.empty() ? "" : "\n") + ("restore previous TLS file " + old.path + ": " + e);
		}
		return errs;
	};
	for (auto &f : *files) {
		if (auto e = stage_file(f.path, f.data, f.mode, f.staged); !e.empty()) {
			cleanup();
			return "stage TLS file " + f.path + ": " + e;
		}
	}
	for (size_t i = 0; i < files->size(); i++) {
		File &f = (*files)[i];
		std::string e;
		if (::rename(f.staged.c_str(), f.path.c_str()) != 0) e = path_error("rename", f.staged, errno);
		else {
			f.staged.clear();
			e = sync_dir(dir_of(f.path));
		}
		if (!e.empty()) {
			// A directory-sync failure may come after the rename: restore this file and every earlier one.
			cleanup();
			std::string r = restore_up_to(i);
			return "write TLS file " + f.path + ": " + e + (r.empty() ? "" : "\n" + r);
		}
	}
	cleanup();
	restore = [restore_up_to, n = files->size()] { return restore_up_to(n - 1); };
	return "";
}

std::string status_json(const config::TlsSettings &conf, Env &e)
{
	config::Cert cert = conf.cert.with_defaults();
	const bool enabled = conf.proto == "https";
	const bool custom = cert.crt != e.paths.default_crt || cert.key != e.paths.default_key;
	const char *source = !custom ? "generated" : cert.crt == e.paths.acme_crt && cert.key == e.paths.acme_key ? "acme" : "custom";
	std::string error, certificate, served, certificate_fp, served_fp;
	Instant at = now();
	if (auto pair = load_certificate_pair(cert.crt, cert.key)) {
		certificate = describe(pair->pair, at);
		certificate_fp = fingerprint(pair->pair);
	} else {
		error = pair.error();
	}
	bool pending = false;
	if (enabled) {
		Served s = e.store->current();
		if (s.pair) {
			served = describe(s.pair->pair, at);
			served_fp = fingerprint(s.pair->pair);
		}
		if (!s.fallback.empty() && error.empty()) error = s.fallback;
		// The configured pair is not the served one: a reload (or a restart) is due, or the generated pair stands
		// in for a broken custom one.
		pending = !certificate.empty() && (served.empty() || served_fp != certificate_fp);
	}
	std::string out = std::string("{\"enabled\":") + (enabled ? "true" : "false") + ",\"custom\":" + (custom ? "true" : "false") +
	                  ",\"source\":\"" + source + "\",\"certFile\":" + gojson::quote(cert.crt) +
	                  ",\"keyFile\":" + gojson::quote(cert.key);
	if (!certificate.empty()) out += ",\"certificate\":" + certificate;
	if (!served.empty()) out += ",\"served\":" + served;
	out += std::string(",\"pending\":") + (pending ? "true" : "false");
	if (!error.empty()) out += ",\"error\":" + gojson::quote(error);
	return out + "}";
}

Answer fail(int code, std::string msg) { return Answer{code, std::move(msg), {}}; }

// Runs fn on the worker (one thread, in order: the service's lock) and answers the request with its result.
void answer_later(http::Request &req, http::Response &res, std::function<Answer()> fn, std::function<void()> after_answer = {})
{
	res.pending = true;
	loop::work([id = req.id, headers = res.headers, fn = std::move(fn), after_answer = std::move(after_answer)] {
		Answer a = fn();
		http::Response out;
		out.headers = headers;
		out.json(200, answer_json(a));
		http::complete(id, std::move(out));
		if (after_answer && a.code == 0) after_answer();
	});
}

// The served pair goes to the listener on the loop thread; the worker waits for it.
bool serve_on_loop(const Parsed &pair)
{
	auto key = serving_key_pem(pair.pair.key);
	if (!key) return false;
	auto done = std::make_shared<std::promise<bool>>();
	auto result = done->get_future();
	loop::post([done, cert = pair.cert_out, key = *key] { done->set_value(http::serve_certificate(cert, key)); });
	return result.wait_for(std::chrono::seconds(10)) == std::future_status::ready && result.get();
}

// The pair of the store goes to the listener at once (it could not be started with the files).
bool serve_pending = false;

// OpenSSL loads the pair from the files as the listener will.
bool listener_loads(const std::string &crt, const std::string &key)
{
	std::unique_ptr<SSL_CTX, decltype(&SSL_CTX_free)> ctx(SSL_CTX_new(TLS_server_method()), SSL_CTX_free);
	return ctx && SSL_CTX_use_certificate_chain_file(ctx.get(), crt.c_str()) == 1 &&
	       SSL_CTX_use_PrivateKey_file(ctx.get(), key.c_str(), SSL_FILETYPE_PEM) == 1 &&
	       SSL_CTX_check_private_key(ctx.get()) == 1;
}

// /etc/init.d/S95nanokvm restart, detached from the server it stops: its own session, no inherited descriptors.
void restart_service()
{
	pid_t pid = ::fork();
	if (pid < 0) {
		log::error("failed to restart the service: {}", std::strerror(errno));
		return;
	}
	if (pid == 0) {
		::setsid();
		if (::fork() != 0) ::_exit(0);
		int null = ::open("/dev/null", O_RDWR);
		if (null >= 0) {
			::dup2(null, 0);
			::dup2(null, 1);
			::dup2(null, 2);
		}
		process::cloexec_inherited();
		::execl("/bin/sh", "sh", "-c", "/etc/init.d/S95nanokvm restart", static_cast<char *>(nullptr));
		::_exit(127);
	}
	int status = 0;
	::waitpid(pid, &status, 0);
}

} // namespace

std::string answer_json(const Answer &a)
{
	return "{\"code\":" + std::to_string(a.code) + ",\"msg\":" + gojson::quote(a.msg) + ",\"data\":" +
	       (a.data.empty() ? "null" : a.data) + "}";
}

std::string Store::load(const std::string &crt, const std::string &key)
{
	auto pair = load_certificate_pair(crt, key);
	if (pair && serve && !serve(*pair)) {
		std::lock_guard lock(mutex_);
		served_.error = "the HTTPS listener cannot use the certificate";
		return served_.error;
	}
	std::lock_guard lock(mutex_);
	if (!pair) {
		served_.error = pair.error();
		return pair.error();
	}
	served_.pair = std::make_shared<const Parsed>(std::move(*pair));
	served_.crt = crt;
	served_.key = key;
	served_.error.clear();
	served_.fallback.clear();
	return "";
}

void Store::mark_fallback(std::string why)
{
	std::lock_guard lock(mutex_);
	served_.fallback = std::move(why);
}

Served Store::current() const
{
	std::lock_guard lock(mutex_);
	return served_;
}

Store &server_certificate()
{
	static Store store;
	return store;
}

std::string ensure_server_certificate(const std::string &crt, const std::string &key, const Paths &paths, Instant at)
{
	if (crt == paths.default_crt && key == paths.default_key) return ensure_generated(crt, key, at);
	if (!load_x509_key_pair(crt, key)) return "custom TLS certificate is missing or invalid";
	return "";
}

std::string prepare_server_certificate(const std::string &crt, const std::string &key, Store &store, const Paths &paths, Instant at)
{
	if (crt == paths.default_crt && key == paths.default_key) {
		if (auto e = ensure_generated(crt, key, at); !e.empty()) return e;
		return store.load(crt, key);
	}
	std::string custom_error = store.load(crt, key);
	if (custom_error.empty()) return "";
	log::error("the configured HTTPS certificate {} cannot be used ({}); serving the generated certificate instead", crt, custom_error);
	if (auto e = ensure_generated(paths.default_crt, paths.default_key, at); !e.empty()) return e;
	if (auto e = store.load(paths.default_crt, paths.default_key); !e.empty()) return e;
	store.mark_fallback(crt + " cannot be used (" + custom_error + "); the generated certificate is served");
	return "";
}

Env &env()
{
	static Env e = [] {
		Env v;
		v.read_config = [] { return config::read_tls(); };
		v.update_config = [](const std::string &proto, const config::Cert &cert) {
			std::string error;
			if (!config::update_tls(proto, cert, error)) {
				log::error("failed to update the TLS configuration: {}", error);
				return false;
			}
			return true;
		};
		v.store = &server_certificate();
		v.clock = [] { return now(); };
		v.ensure_generated = [] {
			const Paths &p = env().paths;
			return ensure_server_certificate(p.default_crt, p.default_key, p, now());
		};
		return v;
	}();
	return e;
}

Answer get_status(Env &e)
{
	auto conf = e.read_config();
	if (!conf) return fail(-1, "cannot read the configuration");
	return Answer{0, "success", status_json(*conf, e)};
}

Answer upload(Env &e, std::string_view cert_pem, std::string_view key_pem)
{
	auto parsed = parse_certificate_pair(cert_pem, key_pem);
	if (!parsed) return fail(-1, parsed.error());
	const Certificate &leaf = parsed->pair.leaf;
	Instant t = e.clock();
	if (after(t, leaf.not_after)) return fail(-1, "the certificate expired on " + format_date(leaf.not_after));
	if (t.sec < leaf.not_before) return fail(-1, "the certificate is not valid before " + format_date(leaf.not_before));

	auto conf = e.read_config();
	if (!conf) return fail(-2, "cannot read the configuration");
	std::function<std::string()> restore;
	if (auto err = store_custom_pair(e.paths, parsed->cert_out, parsed->key_out, restore); !err.empty()) {
		log::error("failed to store the custom TLS certificate: {}", err);
		return fail(-2, "cannot store the certificate");
	}
	config::Cert next{e.paths.custom_crt, e.paths.custom_key};
	if (!e.update_config(conf->proto, next)) {
		// A directory-sync error can follow a committed configuration rename: restore the previous selection
		// before restoring the pair.
		std::string err = "update failed";
		if (!e.update_config(conf->proto, conf->cert)) err += "\nrestore previous TLS configuration failed";
		if (auto r = restore(); !r.empty()) err += "\n" + r;
		log::error("failed to point the configuration at the custom TLS certificate: {}", err);
		return fail(-2, "cannot update the configuration");
	}
	conf->cert = next;
	if (conf->proto == "https") {
		if (auto err = e.store->load(e.paths.custom_crt, e.paths.custom_key); !err.empty()) {
			log::error("failed to activate the custom TLS certificate: {}", err);
			return fail(-3, "the certificate is stored but could not be activated: " + err);
		}
	}
	log::info("custom TLS certificate installed: {}", leaf.subject_cn);
	return Answer{0, "success", status_json(*conf, e)};
}

Answer remove(Env &e)
{
	auto conf = e.read_config();
	if (!conf) return fail(-2, "cannot read the configuration");
	if (conf->proto == "https") {
		if (auto err = e.ensure_generated(); !err.empty()) {
			log::error("failed to prepare the generated TLS certificate: {}", err);
			return fail(-2, "cannot prepare the generated certificate");
		}
		if (auto err = e.store->load(e.paths.default_crt, e.paths.default_key); !err.empty()) {
			log::error("failed to activate the generated TLS certificate: {}", err);
			return fail(-2, "cannot activate the generated certificate");
		}
	}
	conf->cert = config::Cert{e.paths.default_crt, e.paths.default_key};
	if (!e.update_config(conf->proto, conf->cert)) {
		log::error("failed to point the configuration at the generated TLS certificate");
		return fail(-2, "cannot update the configuration");
	}
	for (const std::string &path : {e.paths.custom_key, e.paths.custom_crt})
		if (::unlink(path.c_str()) != 0 && errno != ENOENT) log::error("failed to remove {}: {}", path, path_error("remove", path, errno));
	log::info("custom TLS certificate removed; the generated one is in use");
	return Answer{0, "success", status_json(*conf, e)};
}

Answer reload(Env &e)
{
	auto conf = e.read_config();
	if (!conf) return fail(-2, "cannot read the configuration");
	if (conf->proto != "https") return fail(-1, "HTTPS is off");
	config::Cert cert = conf->cert.with_defaults();
	if (auto err = e.store->load(cert.crt, cert.key); !err.empty()) {
		log::error("failed to reload the TLS certificate: {}", err);
		return fail(-3, err);
	}
	return Answer{0, "success", status_json(*conf, e)};
}

std::string set_tls(Env &e, bool enabled)
{
	auto conf = e.read_config();
	if (!conf) return "cannot read the configuration";
	if (enabled) {
		conf->cert = conf->cert.with_defaults();
		if (auto err = ensure_server_certificate(conf->cert.crt, conf->cert.key, e.paths, now()); !err.empty()) return err;
		conf->proto = "https";
	} else {
		conf->proto = "http";
	}
	if (!e.update_config(conf->proto, conf->cert)) return "cannot update the configuration";
	return "";
}

std::optional<std::string> prepare(const config::Config &conf)
{
	if (conf.proto != "https") return std::nullopt;
	Env &e = env();
	const config::Cert cert = conf.cert.with_defaults();
	if (auto err = prepare_server_certificate(cert.crt, cert.key, *e.store, e.paths, now()); !err.empty()) return err;
	Served s = e.store->current();
	if (listener_loads(s.crt, s.key)) {
		http::use_certificate_files(s.crt, s.key);
		return std::nullopt;
	}
	// A pair the Go rules accept but OpenSSL does not read from its files (an unusual PEM label): the listener
	// starts with the generated pair and is handed the served pair from memory once it runs.
	if (auto err = ensure_generated(e.paths.default_crt, e.paths.default_key, now()); !err.empty()) return err;
	http::use_certificate_files(e.paths.default_crt, e.paths.default_key);
	serve_pending = !(s.crt == e.paths.default_crt && s.key == e.paths.default_key);
	return std::nullopt;
}

void start()
{
	Store &store = *env().store;
	if (serve_pending) {
		serve_pending = false;
		Served s = store.current();
		auto key = s.pair ? serving_key_pem(s.pair->pair.key) : std::nullopt;
		if (!key || !http::serve_certificate(s.pair->cert_out, *key))
			log::error("the HTTPS listener cannot use the certificate {}; the generated certificate is served", s.crt);
	}
	store.serve = serve_on_loop;
}

void add_routes(http::Router &router)
{
	const auth::Guard admin{{"admin"}};
	const auth::Guard admin_recent{{"admin"}, true};

	router.add("POST", "/api/vm/tls", auth::guarded([](http::Request &req, http::Response &res) {
		// proto.SetTlsReq {Enabled bool}: JSON matches the field name without regard to case, a form the name itself.
		auto params = api::Params::bind(req);
		if (!params) return res.json(200, answer_json(fail(-1, "invalid arguments: invalid request body")));
		bool ok = true;
		std::optional<bool> enabled;
		for (const char *name : {"enabled", "Enabled", "ENABLED"}) {
			if (enabled || !ok) break;
			enabled = params->boolean(name, ok);
		}
		if (!ok)
			return res.json(200, answer_json(fail(-1, "invalid arguments: json: cannot unmarshal into Go struct field SetTlsReq.Enabled of type bool")));
		answer_later(
			req, res,
			[on = enabled.value_or(false)] {
				if (auto err = set_tls(env(), on); !err.empty()) {
					log::error("failed to set TLS: {}", err);
					return fail(-2, "operation failed");
				}
				return Answer{};
			},
			// The restart stops this process: the answer goes out first, the restart a second later.
			[] { loop::post([] { loop::after(std::chrono::seconds(1), [] { loop::blocking(restart_service); }); }); });
	}, admin));

	router.add("GET", "/api/vm/tls", auth::guarded([](http::Request &req, http::Response &res) {
		answer_later(req, res, [] { return get_status(env()); });
	}, admin));

	router.add("POST", "/api/vm/tls/certificate", auth::guarded([](http::Request &req, http::Response &res) {
		// Form fields "cert" (PEM: the chain, leaf first, may include the key) and "key" (optional when "cert" has it).
		auto parts = req.body.size() <= max_certificate_upload ? api::multipart(req) : std::nullopt;
		const api::Part *cert = parts ? api::form_file(*parts, "cert") : nullptr;
		if (!cert) return res.json(200, answer_json(fail(-1, "upload the certificate file (PEM)")));
		const api::Part *key = api::form_file(*parts, "key");
		std::string cert_pem(cert->data), key_pem(key ? key->data : cert->data);
		answer_later(req, res, [cert_pem = std::move(cert_pem), key_pem = std::move(key_pem)] {
			return upload(env(), cert_pem, key_pem);
		});
	}, admin_recent));

	router.add("POST", "/api/vm/tls/certificate/delete", auth::guarded([](http::Request &req, http::Response &res) {
		answer_later(req, res, [] { return remove(env()); });
	}, admin_recent));

	router.add("POST", "/api/vm/tls/reload", auth::guarded([](http::Request &req, http::Response &res) {
		answer_later(req, res, [] { return reload(env()); });
	}, admin));
}

} // namespace nkos::system::tls
