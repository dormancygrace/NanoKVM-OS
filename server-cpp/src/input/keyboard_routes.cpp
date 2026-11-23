#include "input/keyboard_routes.hpp"

#include "api/request.hpp"
#include "api/response.hpp"
#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "common/time.hpp"
#include "config/config.hpp"
#include "input/control.hpp"
#include "input/hid.hpp"
#include "input/ws.hpp"

#include <glaze/glaze.hpp>
#include <openssl/rand.h>

#include <cerrno>
#include <format>
#include <fstream>
#include <map>
#include <sstream>
#include <unistd.h>

namespace nkos::input {

namespace {

using namespace std::chrono_literals;
using Clock = std::chrono::steady_clock;
using Report = std::vector<std::uint8_t>;

struct KeyChar {
	std::uint8_t modifiers;
	std::uint8_t code;
};

#include "input/keymaps.inc"

constexpr int input_owner_code = -4;
constexpr const char *lease_header = "x-nanokvm-input-lease";
constexpr const char *shortcut_file = "/etc/kvm/shortcuts.json";
constexpr const char *leader_key_file = "/etc/kvm/leader-key";

std::map<char32_t, KeyChar> layout(const std::string &lang)
{
	std::map<char32_t, KeyChar> m;
	for (const auto &[r, c] : us_map) m[r] = c;
	const std::vector<std::pair<char32_t, KeyChar>> *over = lang == "de" ? &de_map : lang == "fr" ? &fr_map : lang == "es" ? &es_map : nullptr;
	if (over)
		for (const auto &[r, c] : *over) m[r] = c;
	return m;
}

std::vector<char32_t> utf8_runes(const std::string &s)
{
	std::vector<char32_t> out;
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		int len = c < 0x80 ? 1 : (c >> 5) == 6 ? 2 : (c >> 4) == 14 ? 3 : (c >> 3) == 30 ? 4 : 1;
		char32_t r = len == 1 ? (c < 0x80 ? c : 0xfffd) : c & (0x7f >> len);
		bool ok = i + static_cast<std::size_t>(len) <= s.size();
		for (int k = 1; ok && k < len; k++) {
			unsigned char d = static_cast<unsigned char>(s[i + static_cast<std::size_t>(k)]);
			if ((d & 0xc0) != 0x80) ok = false;
			else r = r << 6 | (d & 0x3f);
		}
		if (!ok) {
			r = 0xfffd;
			len = 1;
		}
		out.push_back(r);
		i += static_cast<std::size_t>(len);
	}
	return out;
}

// strconv.QuoteRune of a rune for the error message.
std::string quote_rune(char32_t r)
{
	if (r == '\'') return "'\\''";
	if (r == '\\') return "'\\\\'";
	if (r == '\n') return "'\\n'";
	if (r == '\t') return "'\\t'";
	if (r == '\r') return "'\\r'";
	if (r == '\a') return "'\\a'";
	if (r == '\b') return "'\\b'";
	if (r == '\f') return "'\\f'";
	if (r == '\v') return "'\\v'";
	if (r >= 0x80 && r < 0xa0) return std::format("'\\u{:04x}'", static_cast<unsigned>(r));
	if (r < 0x20 || r == 0x7f) return std::format("'\\x{:02x}'", static_cast<unsigned>(r));
	std::string out = "'";
	if (r < 0x80) out += static_cast<char>(r);
	else if (r < 0x800) out += static_cast<char>(0xc0 | r >> 6), out += static_cast<char>(0x80 | (r & 0x3f));
	else if (r < 0x10000)
		out += static_cast<char>(0xe0 | r >> 12), out += static_cast<char>(0x80 | (r >> 6 & 0x3f)), out += static_cast<char>(0x80 | (r & 0x3f));
	else
		out += static_cast<char>(0xf0 | r >> 18), out += static_cast<char>(0x80 | (r >> 12 & 0x3f)),
			out += static_cast<char>(0x80 | (r >> 6 & 0x3f)), out += static_cast<char>(0x80 | (r & 0x3f));
	return out + "'";
}

Report keyboard_report(std::uint8_t modifiers, std::initializer_list<std::uint8_t> keys = {})
{
	Report r(8, 0);
	r[0] = modifiers;
	std::size_t i = 2;
	for (auto k : keys) r[i++] = k;
	return r;
}

bool is_release(const Report &r) { return r[0] == 0 && r[2] == 0; }

struct Step {
	Report report;
	std::chrono::milliseconds hold{0}, pause{0};
};

// A run of key reports (a paste, a key sequence) on its own manual session: each report goes to the keyboard writer
// gated by the session; a press first checks that the caller still speaks for the tab with input control. A release
// is written only after a press. The last report is always a release.
struct KeyRun : std::enable_shared_from_this<KeyRun> {
	std::vector<Step> steps;
	std::size_t next = 0;
	std::shared_ptr<control::ManualSession> manual = control::ManualSession::create();
	std::shared_ptr<control::Reservation> reservation;
	hid::Lane lane;
	std::function<bool()> owns = [] { return true; };
	std::function<bool()> cancelled = [] { return false; };
	std::function<void(std::string error, bool owner_changed)> finished;
	bool pressed = false, done = false;

	void start() { step(); }

	void write(const Report &r, std::function<void(bool)> then)
	{
		if (is_release(r) && !pressed) return then(true);
		hid::Report report;
		report.data = r;
		report.reservation = reservation;
		report.complete_reservation = false;
		report.complete = [self = shared_from_this(), release = is_release(r), then](bool ok) {
			if (ok) self->pressed = !release;
			then(ok);
		};
		lane.keyboard(std::move(report));
	}

	void finish(std::string error, bool owner_changed = false)
	{
		if (done) return;
		done = true;
		auto self = shared_from_this();
		write(keyboard_report(0), [self, error, owner_changed](bool) {
			self->reservation->complete(error.empty());
			self->manual->close();
			if (self->finished) self->finished(error, owner_changed);
		});
	}

	void step()
	{
		if (done) return;
		if (cancelled()) return finish("canceled");
		if (next == steps.size()) return finish("");
		Step s = steps[next++];
		if (!is_release(s.report) && !owns()) return finish("another session took input control", true);
		write(s.report, [self = shared_from_this(), s](bool ok) {
			if (!ok) return self->finish("write failed");
			auto wait = s.hold + s.pause;
			if (wait.count() == 0) return self->step();
			loop::after(wait, [self] { self->step(); });
		});
	}
};

// --- key sequences ----------------------------------------------------------------------------------------------

constexpr auto sequence_hold = 100ms;
constexpr std::uint8_t ctrl_left = 0x01, alt_left = 0x04, print_screen = 0x46, delete_key = 0x4c;

const std::map<std::string, std::uint8_t> sysrq_keys = {
	{"b", 0x05}, {"c", 0x06}, {"d", 0x07}, {"e", 0x08}, {"f", 0x09}, {"h", 0x0b}, {"i", 0x0c}, {"j", 0x0d},
	{"k", 0x0e}, {"l", 0x0f}, {"m", 0x10}, {"n", 0x11}, {"o", 0x12}, {"p", 0x13}, {"q", 0x14}, {"r", 0x15},
	{"s", 0x16}, {"t", 0x17}, {"u", 0x18}, {"v", 0x19}, {"w", 0x1a}, {"z", 0x1d}, {"0", 0x27}, {"1", 0x1e},
	{"2", 0x1f}, {"3", 0x20}, {"4", 0x21}, {"5", 0x22}, {"6", 0x23}, {"7", 0x24}, {"8", 0x25}, {"9", 0x26},
};

std::vector<Step> sysrq_steps(std::uint8_t key, std::chrono::milliseconds pause)
{
	return {{keyboard_report(alt_left), sequence_hold},
	        {keyboard_report(alt_left, {print_screen}), sequence_hold},
	        {keyboard_report(alt_left, {print_screen, key}), sequence_hold},
	        {keyboard_report(alt_left, {print_screen}), sequence_hold},
	        {keyboard_report(0), 0ms, pause}};
}

struct SequenceResult {
	std::string sequence;
	bool ok = false;
	std::optional<std::string> error;
	std::string finishedAt;
};
struct SequenceStatus {
	bool running = false;
	std::optional<std::string> sequence, startedAt;
	std::optional<int> durationMs, remainingMs;
	std::optional<SequenceResult> lastResult;
};

struct {
	bool running = false;
	std::string sequence;
	WallClock::time_point started_at;
	Clock::time_point started;
	std::chrono::milliseconds duration{0};
	std::optional<SequenceResult> last;
} sequences;

std::string rfc3339_seconds(WallClock::time_point t)
{
	auto s = rfc3339nano(std::chrono::time_point_cast<std::chrono::seconds>(t), true);
	return s;
}

void end_sequence(const std::string &error)
{
	SequenceResult r{sequences.sequence, error.empty(), {}, rfc3339_seconds(WallClock::now())};
	if (!error.empty()) r.error = error;
	sequences.last = r;
	sequences.running = false;
}

// --- shortcuts --------------------------------------------------------------------------------------------------

struct ShortcutKey {
	std::string code, label;
};
struct Shortcut {
	std::string id;
	std::vector<ShortcutKey> keys;
};
struct ShortcutStore {
	std::vector<Shortcut> shortcuts;
};
struct AddShortcutReq {
	std::optional<std::vector<ShortcutKey>> keys;  // required, may be empty (as the Go validator)
};
struct DeleteShortcutReq {
	std::string id;
};
struct PasteSpeed {
	std::chrono::milliseconds hold, gap, limit;
};

ShortcutStore load_shortcuts()
{
	std::ifstream in(shortcut_file);
	if (!in) return {};
	std::string data((std::istreambuf_iterator<char>(in)), {});
	ShortcutStore s;
	if (data.empty()) return s;
	if (glz::read<glz::opts{.error_on_unknown_keys = false}>(s, data)) {
		log::warn("ignoring unreadable {}", shortcut_file);
		return {};
	}
	return s;
}

std::string uuid4()
{
	unsigned char b[16];
	RAND_bytes(b, sizeof b);
	b[6] = (b[6] & 0x0f) | 0x40;
	b[8] = (b[8] & 0x3f) | 0x80;
	std::string s;
	for (int i = 0; i < 16; i++) {
		if (i == 4 || i == 6 || i == 8 || i == 10) s += '-';
		s += std::format("{:02x}", b[i]);
	}
	return s;
}

// The tab with input control, checked once on entry (and again before every press by the run).
std::optional<std::string> owner_lease(http::Request &req, http::Response &res)
{
	std::string lease = req.header(lease_header);
	if (!ws::allows_input_lease(lease)) {
		api::err(res, input_owner_code, "another session holds input control");
		return std::nullopt;
	}
	return lease;
}

template <typename T>
std::string to_json(const T &v)
{
	std::string out;
	(void)glz::write<glz::opts{.skip_null_members = true}>(v, out);
	return out;
}

} // namespace

bool us_key(char32_t r, std::uint8_t &modifiers, std::uint8_t &code)
{
	static const std::map<char32_t, KeyChar> us = layout("");
	auto it = us.find(r);
	if (it == us.end()) return false;
	modifiers = it->second.modifiers;
	code = it->second.code;
	return true;
}

void add_keyboard_routes(http::Router &router)
{
	router.add("POST", "/api/hid/paste", auth::guarded([](http::Request &req, http::Response &res) {
		auto lease = owner_lease(req, res);
		if (!lease) return;
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> content, langue, speed_name;
		if (params) {
			content = params->string("content", ok);
			langue = params->string("langue", ok);
			speed_name = params->string("speed", ok);
		}
		if (!params || !ok || !content || content->empty()) return api::err(res, -1, "invalid arguments");
		static const std::map<std::string, PasteSpeed> speeds = {
			{"", {0ms, 30ms, 25000ms}}, {"normal", {0ms, 30ms, 25000ms}}, {"slow", {30ms, 100ms, 50000ms}}};
		auto speed_it = speeds.find(speed_name.value_or(""));
		if (speed_it == speeds.end()) return api::err(res, -1, "invalid arguments");
		PasteSpeed speed = speed_it->second;
		constexpr std::size_t max_runes = 25000 / 30;
		if (content->size() > max_runes * 4) return api::err(res, -2, "content too long");
		std::string text;
		for (std::size_t i = 0; i < content->size(); i++) {
			if ((*content)[i] == '\r') {
				text += '\n';
				if (i + 1 < content->size() && (*content)[i + 1] == '\n') i++;
			} else {
				text += (*content)[i];
			}
		}
		auto runes = utf8_runes(text);
		if (runes.size() > max_runes) return api::err(res, -2, "content too long");
		std::string lang = langue.value_or("");
		auto map = layout(lang);
		std::map<char32_t, KeyChar> follow;
		if (lang == "es")
			for (const auto &[r, c] : es_dead_key_follow) follow[r] = c;
		std::vector<char32_t> missing;
		for (char32_t r : runes) {
			if (map.count(r) || std::find(missing.begin(), missing.end(), r) != missing.end()) continue;
			missing.push_back(r);
			if (missing.size() > 5) break;
		}
		if (!missing.empty()) {
			std::string list;
			for (std::size_t i = 0; i < missing.size(); i++) {
				if (i == 5) {
					list += ", ...";
					break;
				}
				list += (i ? ", " : "") + quote_rune(missing[i]);
			}
			return api::err(res, -2, "the keyboard layout cannot type " + list);
		}
		std::size_t keys = 0;
		for (char32_t r : runes) keys += 1 + follow.count(r);
		if (std::chrono::milliseconds(static_cast<long>(keys) * (speed.hold + speed.gap).count()) > speed.limit)
			return api::err(res, -2, std::format("the text is too long for this speed: at most {} characters",
			                                     speed.limit.count() / (speed.hold + speed.gap).count()));
		auto run = std::make_shared<KeyRun>();
		run->reservation = run->manual->reserve(control::ReportKind::keyboard, false, true, control::allow_manual);
		if (!run->reservation) {
			log::error("manual paste failed to acquire HID control");
			return api::err(res, -3, "HID control is busy");
		}
		auto press = [&](KeyChar k) {
			run->steps.push_back({keyboard_report(k.modifiers, {k.code}), speed.hold, 0ms});
			run->steps.push_back({keyboard_report(0), 0ms, speed.gap});
		};
		for (char32_t r : runes) {
			press(map[r]);
			if (auto f = follow.find(r); f != follow.end()) press(f->second);
		}
		run->owns = [lease = *lease] { return ws::allows_input_lease(lease); };
		run->cancelled = [id = req.id] { return !http::request_open(id); };
		std::size_t count = runes.size();
		run->finished = [id = req.id, count](std::string error, bool owner_changed) {
			http::Response out;
			if (owner_changed) {
				log::info("hid paste stopped: {}", error);
				api::err(out, input_owner_code, "another session holds input control");
			} else if (!error.empty()) {
				log::error("hid paste failed: {}", error);
				api::err(out, -3, "HID paste failed");
			} else {
				log::debug("hid paste success, total {} characters typed", count);
				api::ok(out);
			}
			http::complete(id, std::move(out));
		};
		res.pending = true;
		run->start();
	}));

	router.add("GET", "/api/hid/sequence", auth::guarded([](http::Request &, http::Response &res) {
		SequenceStatus s;
		s.running = sequences.running;
		s.lastResult = sequences.last;
		if (sequences.running) {
			s.sequence = sequences.sequence;
			s.startedAt = rfc3339_seconds(sequences.started_at);
			s.durationMs = static_cast<int>(sequences.duration.count());
			auto left = sequences.duration - std::chrono::duration_cast<std::chrono::milliseconds>(Clock::now() - sequences.started);
			s.remainingMs = static_cast<int>(std::max(left, 0ms).count());
			if (*s.durationMs == 0) s.durationMs.reset();
			if (*s.remainingMs == 0) s.remainingMs.reset();
		}
		api::send(res, 0, "success", to_json(s));
	}));

	router.add("POST", "/api/hid/sequence", auth::guarded([](http::Request &req, http::Response &res) {
		auto lease = owner_lease(req, res);
		if (!lease) return;
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> sequence, key;
		std::optional<long long> delay_ms;
		if (params) {
			sequence = params->string("sequence", ok);
			key = params->string("key", ok);
			delay_ms = params->integer("delayMs", ok);
		}
		if (!params || !ok || !sequence || (*sequence != "ctrl-alt-del" && *sequence != "sysrq" && *sequence != "reisub"))
			return api::err(res, -1, "invalid arguments");
		std::vector<Step> steps;
		if (*sequence == "ctrl-alt-del") {
			steps = {{keyboard_report(ctrl_left | alt_left, {delete_key}), sequence_hold}, {keyboard_report(0)}};
		} else if (*sequence == "sysrq") {
			auto it = sysrq_keys.find(key.value_or(""));
			if (it == sysrq_keys.end()) return api::err(res, -2, std::format("unknown SysRq key \"{}\"", key.value_or("")));
			steps = sysrq_steps(it->second, 0ms);
		} else {
			std::chrono::milliseconds delay = 5000ms;
			if (delay_ms.value_or(0) != 0) {
				delay = std::chrono::milliseconds(*delay_ms);
				if (delay < 1000ms || delay > 30000ms) return api::err(res, -2, "REISUB delay must be between 1s and 30s");
			}
			const char *reisub[] = {"r", "e", "i", "s", "u", "b"};
			for (int i = 0; i < 6; i++) {
				auto part = sysrq_steps(sysrq_keys.at(reisub[i]), i == 5 ? 0ms : delay);
				steps.insert(steps.end(), part.begin(), part.end());
			}
		}
		std::chrono::milliseconds duration{0};
		for (const auto &s : steps) duration += s.hold + s.pause;
		if (sequences.running) return api::err(res, -3, "HID control is busy");
		sequences.running = true;
		sequences.sequence = *sequence;
		sequences.started_at = WallClock::now();
		sequences.started = Clock::now();
		sequences.duration = duration;
		auto run = std::make_shared<KeyRun>();
		run->steps = std::move(steps);
		run->reservation = run->manual->reserve(control::ReportKind::keyboard, true, true, control::allow_manual);
		if (!run->reservation) {
			end_sequence("manual input is blocked");
			log::error("key sequence {} failed to acquire HID control", *sequence);
			return api::err(res, -3, "HID control is busy");
		}
		std::string started = std::format(R"({{"sequence":{},"durationMs":{}}})", api::quote(*sequence), duration.count());
		bool detached = *sequence == "reisub";
		if (detached) {
			// REISUB runs on after the answer, bounded by its length plus a grace.
			auto deadline = Clock::now() + duration + 10s;
			run->cancelled = [deadline] { return Clock::now() > deadline; };
		} else {
			run->owns = [lease = *lease] { return ws::allows_input_lease(lease); };
			run->cancelled = [id = req.id] { return !http::request_open(id); };
		}
		run->finished = [id = req.id, detached, started, name = *sequence](std::string error, bool owner_changed) {
			end_sequence(error);
			if (owner_changed) log::info("key sequence {} stopped: {}", name, error);
			else if (!error.empty()) log::error("key sequence {} failed: {}", name, error);
			if (detached) return;
			http::Response out;
			if (owner_changed) api::err(out, input_owner_code, "another session holds input control");
			else if (!error.empty()) api::err(out, -3, "key sequence failed");
			else api::send(out, 0, "success", started);
			http::complete(id, std::move(out));
		};
		if (detached) api::send(res, 0, "success", started);
		else res.pending = true;
		run->start();
	}));

	router.add("GET", "/api/hid/leds", auth::guarded([](http::Request &, http::Response &res) {
		auto s = hid::led_status();
		// The HTTP answer is in UTC, as the Go server formats it; empty before the host's first report.
		std::string updated = s.updated == WallClock::time_point{} ? "" : rfc3339nano(s.updated, true);
		api::send(res, 0, "success",
		          std::format(R"({{"keyboardEnabled":{},"numLock":{},"capsLock":{},"scrollLock":{},"known":{},"updatedAt":{}}})",
		                      s.keyboard_enabled, s.num_lock, s.caps_lock, s.scroll_lock, s.known, api::quote(updated)));
	}));

	router.add("GET", "/api/hid/shortcuts", auth::guarded([](http::Request &, http::Response &res) {
		api::send(res, 0, "success", to_json(load_shortcuts()));
	}));
	router.add("GET", "/api/hid/shortcut/leader-key", auth::guarded([](http::Request &, http::Response &res) {
		std::ifstream in(leader_key_file);
		if (!in) {
			if (errno == ENOENT) return api::send(res, 0, "success", R"({"key":""})");
			return api::err(res, -1, "read leader key failed");
		}
		std::string key((std::istreambuf_iterator<char>(in)), {});
		std::erase(key, '\n');
		api::send(res, 0, "success", std::format(R"({{"key":{}}})", api::quote(key)));
	}));

	auth::Guard admin{{"admin"}};
	router.add("POST", "/api/hid/reset", auth::guarded([](http::Request &req, http::Response &res) {
		auto manual = control::ManualSession::create();
		auto reservation = manual->reserve(control::ReportKind::relative_mouse, false, true);
		if (!reservation) return api::err(res, -1, "HID control is busy");
		res.pending = true;
		hid::reset_usb_phy([id = req.id, manual, reservation](std::string error) {
			reservation->complete(error.empty());
			manual->close();
			http::Response out;
			if (!error.empty()) {
				log::error("failed to reset hid: {}", error);
				api::err(out, -1, "failed to reset hid");
			} else {
				api::ok(out);
			}
			http::complete(id, std::move(out));
		});
	}, admin));
	router.add("POST", "/api/hid/shortcut", auth::guarded([](http::Request &req, http::Response &res) {
		AddShortcutReq r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body) || !r.keys)
			return api::err(res, -1, "invalid arguments");
		auto store = load_shortcuts();
		Shortcut s{uuid4(), *r.keys};
		store.shortcuts.push_back(s);
		if (!config::atomic_write(shortcut_file, to_json(store), 0644)) {
			log::error("failed to add shortcut");
			return api::err(res, -2, "add shortcut failed");
		}
		api::ok(res);
	}, admin));
	router.add("DELETE", "/api/hid/shortcut", auth::guarded([](http::Request &req, http::Response &res) {
		DeleteShortcutReq r;
		if (glz::read<glz::opts{.error_on_unknown_keys = false}>(r, req.body) || r.id.empty())
			return api::err(res, -1, "invalid arguments");
		auto store = load_shortcuts();
		auto before = store.shortcuts.size();
		std::erase_if(store.shortcuts, [&](const Shortcut &s) { return s.id == r.id; });
		if (store.shortcuts.size() == before || !config::atomic_write(shortcut_file, to_json(store), 0644)) {
			log::error("failed to delete shortcut");
			return api::err(res, -2, "delete shortcut failed");
		}
		api::ok(res);
	}, admin));
	router.add("POST", "/api/hid/shortcut/leader-key", auth::guarded([](http::Request &req, http::Response &res) {
		bool ok = true;
		auto params = api::Params::bind(req);
		std::optional<std::string> key;
		if (params) key = params->string("key", ok);
		if (!params || !ok) return api::err(res, -1, "invalid arguments");
		if (key.value_or("").empty()) {
			if (::unlink(leader_key_file) != 0 && errno != ENOENT) return api::err(res, -2, "reset failed");
		} else {
			std::ofstream out(leader_key_file, std::ios::trunc);
			if (!(out << *key)) return api::err(res, -3, "write failed");
		}
		api::ok(res);
	}, admin));
}

} // namespace nkos::input
