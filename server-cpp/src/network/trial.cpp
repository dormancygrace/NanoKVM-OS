// Network changes on trial, as the Go server's service/network/trial.go and internal/networktrial: a change that can
// cut the device off is applied at once and undone unless it is confirmed within two minutes from any address the
// device can still be reached at. The previous settings are kept on the boot partition (/boot/net-trial.json), so a
// restart during the trial reverts too.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"

#include <algorithm>
#include <atomic>
#include <condition_variable>
#include <sys/random.h>
#include <thread>

namespace nkos::network {

namespace {

constexpr auto trial_duration = std::chrono::minutes(2);
constexpr auto trial_retry = std::chrono::seconds(30);
constexpr int code_pending = -1001, code_undoing = -1002;

std::atomic<long> trial_workers{0};

} // namespace

const char *trial_pending_text = "a network change is waiting for confirmation; confirm it or wait for it to be undone";
const char *trial_undoing_text = "an earlier network change is still being undone; try again later";

std::function<void()> trial_work_begin()
{
	trial_workers++;
	auto once = std::make_shared<std::atomic<bool>>(false);
	return [once] {
		if (!once->exchange(true)) trial_workers--;
	};
}

std::optional<std::string> trial_check_idle()
{
	static const std::string pending = "a network trial or rollback is still pending";
	if (trial_workers.load() != 0) return pending;
	auto err = lstat_path(paths.trial_file);
	if (err.not_exist()) return std::nullopt;
	if (err) return "check network trial " + paths.trial_file + ": " + errno_text(err.code);
	return pending + " (" + paths.trial_file + ")";
}

struct Trial {
	std::string id, token;
	TrialRecord record;
	bool started = false;   // the revert is armed (Go: timer != nil)
	bool applied = false;   // the service restarted with the change
	bool reverting = false;
	std::function<void()> release;
	// The revert timer; cancelled when the trial ends otherwise.
	std::mutex timer_mu;
	std::condition_variable timer_cv;
	bool timer_stopped = false;
	void stop_timer()
	{
		std::lock_guard lock(timer_mu);
		timer_stopped = true;
		timer_cv.notify_all();
	}
};

namespace {

std::mutex trial_mu;
TrialPtr active;

std::string random_hex(std::size_t n)
{
	std::string bytes(n, '\0');
	std::size_t got = 0;
	while (got < n) {
		ssize_t r = ::getrandom(bytes.data() + got, n - got, 0);
		if (r > 0) got += static_cast<std::size_t>(r);
	}
	return hex_encode(bytes);
}

Error trial_idle()
{
	if (active) return Error{code_pending, trial_pending_text};
	// A record nobody owns is the only way back to the confirmed settings.
	if (!lstat_path(paths.trial_file).not_exist()) return Error{code_undoing, trial_undoing_text};
	return {};
}

Error restart_service(const std::string &service)
{
	if (service == "ethernet") return restart_ethernet();
	if (service == "wifi-ap") return restart_wifi_ap();
	// The country restored: iw sets it, and Wi-Fi restarts with it.
	if (service == "wifi-country") return restart_wifi_country();
	return {};
}

Error restore_record(const TrialRecord &record, const std::string &reason, bool restart)
{
	log::warn("network change undone ({}): restoring the previous {} settings", reason, record.service);
	if (auto err = restore_files(record.files)) return err;
	if (auto err = sync_files()) return wrap("sync the previous settings", err);
	// The running network must have them too before the record goes.
	if (restart)
		if (auto err = restart_service(record.service)) return wrap("restart " + record.service + " with the previous settings", err);
	if (auto err = remove(paths.trial_file); err && !err.not_exist()) return wrap("remove the trial record", err);
	if (auto err = sync_files()) return wrap("sync the removal of the trial record", err);
	return {};
}

void finish_revert(TrialPtr t, std::string reason, bool restart)
{
	if (auto err = restore_record(t->record, reason, restart)) {
		log::error("failed to restore the previous network settings, trying again in 30s: {}", err.text);
		std::lock_guard lock(trial_mu);
		if (active == t)
			std::thread([t] {
				std::this_thread::sleep_for(trial_retry);
				finish_revert(t, "a retry", true);
			}).detach();
		return;
	}
	std::lock_guard lock(trial_mu);
	if (active == t) {
		active.reset();
		t->release();
	}
}

void revert_trial(const TrialPtr &t, const std::string &reason, bool restart)
{
	{
		std::lock_guard lock(trial_mu);
		if (active != t || t->reverting) return;
		t->reverting = true;
		t->stop_timer();
	}
	finish_revert(t, reason, restart);
}

} // namespace

std::string trial_record_json(const TrialRecord &record)
{
	std::string out = "{\"files\":";
	if (record.files_nil) {
		out += "null";
	} else {
		out += "{";
		bool first = true;
		for (const auto &[path, contents] : record.files) {
			out += (first ? "" : ",") + quote(path) + ":" + (contents ? quote(*contents) : std::string("null"));
			first = false;
		}
		out += "}";
	}
	return out + ",\"service\":" + quote(record.service) + "}";
}

std::optional<TrialRecord> parse_trial_record(const std::string &data, std::string &why)
{
	auto v = parse_json_whole(data);
	if (!v) {
		why = "invalid JSON";
		return std::nullopt;
	}
	TrialRecord record;
	record.files_nil = true;
	if (v->kind != JValue::Kind::object && v->kind != JValue::Kind::null) {
		why = "not an object";
		return std::nullopt;
	}
	auto fold = [](std::string s) {
		for (auto &c : s)
			if (c >= 'A' && c <= 'Z') c = static_cast<char>(c - 'A' + 'a');
		return s;
	};
	bool type_error = false;
	for (const auto &[key, member] : v->members) {
		std::string k = fold(key);
		if (k == "files") {
			if (member.kind == JValue::Kind::null) {
				// null sets the map to nil.
				record.files.clear();
				record.files_nil = true;
			} else if (member.kind == JValue::Kind::object) {
				record.files_nil = false;
				for (const auto &[path, contents] : member.members) {
					if (contents.kind == JValue::Kind::null) record.files[path] = std::nullopt;
					else if (contents.kind == JValue::Kind::string) record.files[path] = contents.text;
					else type_error = true;
				}
			} else {
				type_error = true;
			}
		} else if (k == "service") {
			if (member.kind == JValue::Kind::string) record.service = member.text;
			else if (member.kind != JValue::Kind::null) type_error = true;
		}
	}
	if (type_error) {
		why = "wrong type";
		return std::nullopt;
	}
	if (record.files_nil) {
		why = "no files";
		return std::nullopt;
	}
	for (const auto &[path, _] : record.files) {
		if (!trial_path_allowed(path)) {
			why = "it names " + path;
			return std::nullopt;
		}
	}
	return record;
}

bool trial_path_allowed(const std::string &path)
{
	if (path == paths.ethernet_config || path == paths.ethernet_disabled || path == paths.ethernet_vlan) return true;
	auto files = wifi_ap_trial_files();
	return std::find(files.begin(), files.end(), path) != files.end();
}

Error snapshot_files(const std::vector<std::string> &list, FileSnapshot &out)
{
	out.clear();
	for (const auto &path : list) {
		Error err;
		auto data = read_file(path, &err);
		if (err.not_exist()) {
			out[path] = std::nullopt;
			continue;
		}
		if (err) return err;
		out[path] = *data;
	}
	return {};
}

Error restore_files(const FileSnapshot &files)
{
	std::vector<Error> errs;
	for (const auto &[path, contents] : files) {
		if (!contents) {
			if (auto err = remove(path); err && !err.not_exist()) errs.push_back(err);
			continue;
		}
		errs.push_back(write_file_atomic(path, *contents));
	}
	return join_errors(errs);
}

Error without_trial(const std::function<Error()> &write)
{
	std::lock_guard lock(trial_mu);
	if (auto err = trial_idle()) return err;
	return write();
}

bool trial_blocking_error(const Error &err) { return err.code == code_pending || err.code == code_undoing; }

TrialPtr begin_trial(const std::string &service, const std::vector<std::string> &files, Error &err)
{
	std::lock_guard lock(trial_mu);
	if ((err = trial_idle())) return nullptr;
	FileSnapshot snapshot;
	if (auto e = snapshot_files(files, snapshot)) {
		err = wrap("cannot record the current network settings", e);
		return nullptr;
	}
	auto t = std::make_shared<Trial>();
	t->id = random_hex(8);
	t->token = random_hex(16);
	t->record = TrialRecord{snapshot, false, service};
	t->release = trial_work_begin();
	Error e = write_file_atomic(paths.trial_file, trial_record_json(t->record));
	if (!e) e = sync_files();
	if (e) {
		// Nothing has changed yet: no record is better than one not on the card.
		(void)remove(paths.trial_file);
		t->release();
		err = wrap("cannot record the current network settings", e);
		return nullptr;
	}
	active = t;
	err = {};
	return t;
}

void trial_start(const TrialPtr &t)
{
	std::lock_guard lock(trial_mu);
	t->started = true;
	std::thread([t] {
		std::unique_lock timer(t->timer_mu);
		if (t->timer_cv.wait_for(timer, trial_duration, [&] { return t->timer_stopped; })) return;
		timer.unlock();
		revert_trial(t, "not confirmed in time", true);
	}).detach();
}

void trial_restarted(const TrialPtr &t, const Error &err)
{
	if (err) {
		revert_trial(t, "the interface did not restart: " + err.text, true);
		return;
	}
	std::lock_guard lock(trial_mu);
	t->applied = true;
}

void trial_abandon(const TrialPtr &t) { revert_trial(t, "the change could not be saved", false); }

std::string trial_info_json(const TrialPtr &t, const std::vector<std::string> &hosts)
{
	return "{\"id\":" + quote(t->id) + ",\"token\":" + quote(t->token) +
	       ",\"expiresIn\":" + std::to_string(std::chrono::duration_cast<std::chrono::seconds>(trial_duration).count()) +
	       ",\"hosts\":" + json_strings(hosts, true) + "}";
}

Error confirm_trial(const std::string &token, bool &no_trial)
{
	no_trial = false;
	std::lock_guard lock(trial_mu);
	TrialPtr t = active;
	bool match = t && token.size() == t->token.size();
	if (match) {
		unsigned char diff = 0;
		for (std::size_t i = 0; i < token.size(); i++) diff |= static_cast<unsigned char>(token[i] ^ t->token[i]);
		match = diff == 0;
	}
	if (!t || !t->started || !t->applied || t->reverting || !match) {
		no_trial = true;
		return make_error("no network change is waiting for this confirmation");
	}
	if (auto err = remove(paths.trial_file); err && !err.not_exist()) {
		log::error("failed to remove the network trial record: {}", err.text);
		return wrap("cannot keep the change", err);
	}
	if (auto err = sync_files()) {
		// The removal may not be on the card: put the record back, so that a restart and the timer still agree with
		// what was reported.
		(void)write_file_atomic(paths.trial_file, trial_record_json(t->record));
		log::error("failed to sync the confirmation: {}", err.text);
		return wrap("cannot keep the change", err);
	}
	t->stop_timer();
	active.reset();
	t->release();
	log::info("network change confirmed");
	return {};
}

void restore_pending_trial()
{
	Error read_err;
	auto data = read_file(paths.trial_file, &read_err);
	if (read_err.not_exist()) return;
	std::string why;
	std::optional<TrialRecord> record;
	if (data) record = parse_trial_record(*data, why);
	else why = read_err.text;
	if (!record) {
		// Kept aside for a look: the unconfirmed change stays in effect.
		log::error("NETWORK CHANGE NOT UNDONE: the trial record {} is unusable ({}); moved to {}.bad", paths.trial_file, why,
		           paths.trial_file);
		(void)rename(paths.trial_file, paths.trial_file + ".bad");
		(void)sync_files();
		return;
	}
	auto t = std::make_shared<Trial>();
	t->record = *record;
	t->reverting = true;
	t->release = trial_work_begin();
	{
		std::lock_guard lock(trial_mu);
		active = t;
	}
	std::thread([t] { finish_revert(t, "the device restarted before it was confirmed", true); }).detach();
}

std::pair<std::string, bool> probe_trial()
{
	std::lock_guard lock(trial_mu);
	if (active && active->started && !active->reverting) return {active->id, active->applied};
	return {"", false};
}

std::function<std::vector<std::string>()> interface_addrs = [] {
	std::vector<std::string> out;
	for (const auto &iface : interfaces())
		for (const auto &a : iface.addrs) out.push_back(a.str());
	return out;
};

std::function<std::string()> mdns_host = [] {
	if (!exists(paths.avahi_pid)) return std::string();
	auto name = read_file(paths.etc_hostname);
	if (!name) return std::string();
	std::string host = trim_space(*name);
	return host.empty() ? std::string() : host + ".local";
};

std::vector<std::string> trial_hosts(const std::string &new_address, const std::string &request_host)
{
	std::vector<std::string> hosts;
	std::set<std::string> seen;
	auto add = [&](const std::string &host) {
		if (!host.empty() && seen.insert(host).second) hosts.push_back(host);
	};
	add(new_address);
	std::string host, port;
	if (split_host_port(request_host, host, port)) add(trim_chars(host, "[]"));
	else add(trim_chars(request_host, "[]"));
	add(mdns_host());
	std::vector<std::string> others;
	for (const auto &addr : interface_addrs()) {
		auto prefix = parse_prefix(addr);
		if (!prefix || !prefix->addr.is4() || prefix->addr.loopback()) continue;
		others.push_back(prefix->addr.str());
	}
	std::sort(others.begin(), others.end());
	for (const auto &h : others) add(h);
	return hosts;
}

} // namespace nkos::network
