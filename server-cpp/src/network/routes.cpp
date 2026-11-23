// The routes of the Go server's router/network.go. Handlers read what they need of the request on the event loop
// (ingress interface, local address, Host, headers, the account) and do the rest on the blocking pool; a change that
// replaces the interface its reply travels on runs on a thread of its own after the reply, as the Go server's
// goroutines do. The unauthenticated Wi-Fi setup answers refused requests after a penalty timed on the loop.

#include "network/network.hpp"

#include "network/internal.hpp"

#include "auth/session.hpp"
#include "common/log.hpp"
#include "common/loop.hpp"
#include "http/server.hpp"

#include <algorithm>
#include <thread>

namespace nkos::network {

bool synchronous_calls = false;

namespace {

// A goroutine with a sleep: fn on a thread of its own after delay.
void go_after(std::chrono::milliseconds delay, std::function<void()> fn)
{
	std::thread([delay, fn = std::move(fn)] {
		std::this_thread::sleep_for(delay);
		fn();
	}).detach();
}

std::string bool_json(bool v) { return v ? "true" : "false"; }

// Answers from fn on the blocking pool, or at once while operations run synchronously (the differential check
// calls them directly).
void defer_call(const Call &call, http::Response &res, std::function<void(http::Response &)> fn)
{
	if (synchronous_calls) return fn(res);
	res.pending = true;
	loop::blocking([id = call.id, headers = res.headers, fn = std::move(fn)] {
		http::Response out;
		out.headers = headers;
		fn(out);
		http::complete(id, std::move(out));
	});
}

void refuse(http::Response &res, const Refusal &r) { fail(res, r.code, r.msg); }

// checkRadio: reserves the radio, or answers why not.
bool check_radio(http::Response &res, bool need_enabled)
{
	bool available = wifi_control.hardware();
	if (need_enabled) available = wifi_control.present() && wifi_control.enabled();
	// While Wi-Fi is the administrator's access point, it joins no network; the switch and the band preference work.
	if (!available || is_ap_mode() || (need_enabled && wifi_ap_active())) {
		fail(res, -1, "Wi-Fi unavailable");
		return false;
	}
	if (!reserve_wifi()) {
		fail(res, -2, "Wi-Fi busy");
		return false;
	}
	return true;
}

// The no-login setup routes act only during AP provisioning, for a client of the device's own access point, with
// the AP key the OLED shows; refusals wait two seconds.
bool check_ap_request(http::Request &req, http::Response &res)
{
	auto refuse_later = [&](int code, const char *msg) {
		http::Response out;
		out.headers = res.headers;
		fail(out, code, msg);
		res.pending = true;
		loop::after(std::chrono::seconds(2), [id = req.id, out = std::move(out)]() mutable { http::complete(id, std::move(out)); });
		return false;
	};
	auto ingress = http::ingress_interface(req);
	if (!(is_supported() && is_ap_mode() && ingress && *ingress == "wlan0")) return refuse_later(-1, "invalid mode");
	auto [ok, locked] = ap_guard.allow(req.header("x-ap-key"), ap_password());
	if (locked) return refuse_later(-5, "too many attempts: restart Wi-Fi provisioning on the device");
	if (!ok) return refuse_later(-4, "unauthorized");
	return true;
}

Error connect_legacy(const std::string &ssid, const std::string &password)
{
	// Legacy/AP provisioning selects automatic bands and personal security.
	for (const char *name : {"wifi.disabled", "wifi.security", "wifi.hidden", "wifi.freq_list"})
		if (auto err = remove(path_join(paths.etc_kvm, name)); err && !err.not_exist()) return err;
	if (auto err = write_private(paths.wifi_ssid, ssid)) {
		log::error("failed to save wifi ssid: {}", err.text);
		return err;
	}
	if (auto err = write_private(paths.wifi_pass, password)) {
		log::error("failed to save wifi password: {}", err.text);
		return err;
	}
	// The legacy and first-setup routes join automatically, as before.
	if (auto err = wifi_control.remember_joined(legacy_profile(ssid, password), true)) {
		log::error("failed to save wifi network: {}", err.text);
		return err;
	}
	if (auto err = write_file(paths.wifi_connect, "", 0644)) {
		log::error("failed to connect wifi: {}", err.text);
		return err;
	}
	return {};
}

Error run_wifi_script(const char *action)
{
	auto r = wifi_control.run(wifi_script, {action});
	return r.ok() ? Error{} : make_error(r.error);
}


std::mutex ethernet_apply_mu;
bool ethernet_applying = false;
constexpr const char *ethernet_applying_text = "the previous Ethernet change is still being applied; try again in a moment";

bool reserve_ethernet_apply()
{
	std::lock_guard lock(ethernet_apply_mu);
	if (ethernet_applying) return false;
	ethernet_applying = true;
	return true;
}

void release_ethernet_apply()
{
	std::lock_guard lock(ethernet_apply_mu);
	ethernet_applying = false;
}

// --- Wi-Fi -------------------------------------------------------------------------------------------------------------

// ConnectWifiNoAuth after the AP key check: the answer, and how long it waits before it goes out. A joined network
// keeps the radio reserved during the wait (joined: release it then).
std::chrono::milliseconds connect_wifi_op(const RequestData &data, http::Response &out, bool &joined)
{
	joined = false;
	auto decoded = decode_connect_wifi_no_auth(data);
	if (!decoded.value) {
		fail(out, -2, "invalid parameters");
		return std::chrono::seconds(1);
	}
	auto [ssid, password] = *decoded.value;
	if (legacy_profile(ssid, password).validate()) {
		fail(out, -2, "invalid parameters");
		return {};
	}
	if (!reserve_wifi()) {
		fail(out, -2, "Wi-Fi busy");
		return {};
	}
	// A network joined from the setup access point wins over the administrator's access point, which S30wifi would
	// start instead.
	if (auto err = remove(wifi_ap_file("enabled")); err && !err.not_exist())
		log::warn("failed to turn the Wi-Fi access point off: {}", err.text);
	if (connect_legacy(ssid, password)) {
		finish_wifi({});
		fail(out, -3, "failed to connect wifi");
		return {};
	}
	ok(out);
	joined = true;
	return std::chrono::seconds(5);
}

void connect_wifi_no_auth(http::Request &req, http::Response &res)
{
	if (!check_ap_request(req, res)) return;
	res.pending = true;
	loop::blocking([id = req.id, headers = res.headers, data = request_data(req)] {
		http::Response out;
		out.headers = headers;
		bool joined = false;
		auto delay = connect_wifi_op(data, out, joined);
		if (delay == std::chrono::milliseconds::zero()) return http::complete(id, std::move(out));
		loop::post([id, delay, joined, out = std::move(out)]() mutable {
			loop::after(delay, [id, joined, out = std::move(out)]() mutable {
				if (joined) {
					finish_wifi({});
					log::debug("set wifi ap mode successfully");
				}
				http::complete(id, std::move(out));
			});
		});
	});
}

void get_wifi(const Call &call, http::Response &res)
{
	defer_call(call, res, [o = call](http::Response &out) {
		auto status = wifi_control.status();
		status.this_connection = request_may_ride_wifi(o.local);
		ok(out, wifi_status_json(status));
	});
}

void scan_wifi(const Call &call, http::Response &res)
{
	std::string band = go_query(call.data.query, "band");
	if (band != "2.4" && band != "5" && band != "all") return fail(res, -1, "invalid band");
	defer_call(call, res, [band](http::Response &out) {
		if (!check_radio(out, true)) return;
		auto available = wifi_control.frequencies();
		auto freqs = available[band];
		if (band == "all") {
			freqs = available["2.4"];
			freqs.insert(freqs.end(), available["5"].begin(), available["5"].end());
		}
		if (freqs.empty()) {
			finish_wifi({});
			return fail(out, -1, "band unavailable");
		}
		std::vector<std::string> args{"dev", "wlan0", "scan", "freq"};
		args.insert(args.end(), freqs.begin(), freqs.end());
		auto r = wifi_control.run("iw", args);
		finish_wifi({});
		if (!r.ok()) return fail(out, -1, "scan failed");
		ok(out, wifi_networks_json(parse_scan(r.out, band)));
	});
}

void set_wifi_enabled(const Call &call, http::Response &res)
{
	auto decoded = decode_set_wifi_enabled(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [enabled = *decoded.value](http::Response &out) {
		if (!check_radio(out, false)) return;
		bool previous = wifi_control.enabled();
		// Acknowledge before changing the interface carrying this HTTP request.
		ok(out);
		go_after(std::chrono::milliseconds(300), [enabled, previous] {
			std::string path = path_join(wifi_control.etc, "wifi.disabled");
			Error err;
			if (enabled) {
				err = remove(path);
				if (err.not_exist()) err = {};
			} else {
				err = write_private(path, "");
			}
			if (!err) err = run_wifi_script(enabled ? "restart" : "disable");
			if (err) {
				if (previous) (void)remove(path);
				else (void)write_private(path, "");
			}
			finish_wifi(err);
		});
	});
}

void set_wifi_band_preference(const Call &call, http::Response &res)
{
	auto decoded = decode_set_wifi_band_preference(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [band = *decoded.value](http::Response &out) {
		if (!check_radio(out, false)) return;
		// Do not restart wpa_supplicant here: the device may be managed over this Wi-Fi. The running configuration
		// follows the preference instead, and at worst the link reassociates once on the preferred band.
		if (auto err = wifi_control.save_preferred_band(band)) {
			finish_wifi(err);
			return fail(out, -1, "failed to save Wi-Fi preference");
		}
		ok(out);
		if (!wifi_control.enabled()) {
			finish_wifi({});
			return;
		}
		go_after(std::chrono::milliseconds(300), [band] {
			// The saved preference stands even if the running supplicant cannot follow it now.
			if (auto err = wifi_control.apply_preferred_band(band))
				log::warn("Wi-Fi band preference saved; applying it now failed: {}", err.text);
			finish_wifi({});
		});
	});
}

void configure_wifi(const Call &call, http::Response &res)
{
	auto decoded = decode_configure_wifi(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [p = *decoded.value](http::Response &out) {
		if (!check_radio(out, true)) return;
		auto freqs = wifi_control.frequencies();
		if (freqs["2.4"].empty() && freqs["5"].empty()) {
			finish_wifi({});
			return fail(out, -1, "no supported bands");
		}
		std::string preferred = p.preferred_band;
		if (preferred.empty()) preferred = p.band;  // compatibility with the former hard band selector
		if (preferred.empty()) preferred = wifi_control.preferred_band();
		Error err = wifi_control.save_profile(p, freqs, preferred);
		if (!err) err = wifi_control.remember_joined(p, p.auto_connect_or_default());
		if (err) {
			finish_wifi(err);
			return fail(out, -1, "failed to save Wi-Fi");
		}
		ok(out);
		go_after(std::chrono::milliseconds(300), [] { finish_wifi(run_wifi_script("restart")); });
	});
}

void disconnect_wifi(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		if (!check_radio(out, true)) return;
		ok(out);
		go_after(std::chrono::milliseconds(300), [] {
			Error err = wifi_control.migrate_saved();
			if (!err) err = wifi_control.stop_joining_associated();
			// disable also drops the addresses of the left network.
			if (!err) err = run_wifi_script("disable");
			if (!err) err = run_wifi_script("start");
			finish_wifi(err);
		});
	});
}

void get_saved_wifi(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		if (auto err = wifi_control.migrate_saved()) log::warn("Wi-Fi: could not store the configured network as a saved one: {}", err.text);
		auto current = wifi_control.associated().value_or("");
		Error err;
		auto networks = wifi_control.read_saved(current, &err);
		if (err) return fail(out, -1, "failed to read saved networks");
		ok(out, saved_networks_json(networks));
	});
}

void connect_saved_wifi(const Call &call, http::Response &res)
{
	auto decoded = decode_saved_wifi(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [ssid = *decoded.value](http::Response &out) {
		if (!check_radio(out, true)) return;
		WifiProfile p;
		bool auto_connect = false;
		Error err;
		if (!wifi_control.saved_profile(ssid, p, auto_connect, err) || p.validate()) {
			finish_wifi({});
			return fail(out, -1, "unknown network");
		}
		auto freqs = wifi_control.frequencies();
		err = wifi_control.save_profile(p, freqs, wifi_control.preferred_band());
		if (!err) err = wifi_control.remember_joined(p, auto_connect);
		if (err) {
			finish_wifi(err);
			return fail(out, -1, "failed to save Wi-Fi");
		}
		ok(out);
		go_after(std::chrono::milliseconds(300), [] { finish_wifi(run_wifi_script("restart")); });
	});
}

void forget_saved_wifi(const Call &call, http::Response &res)
{
	auto decoded = decode_saved_wifi(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [ssid = *decoded.value](http::Response &out) {
		if (!reserve_wifi()) return fail(out, -2, "Wi-Fi busy");
		std::string name = saved_name(ssid);
		std::string associated = wifi_control.associated().value_or("");
		// The last chosen network (wifi.ssid) is not necessarily the connected one; its top-level copy goes with it.
		bool last_chosen = read_trimmed(path_join(wifi_control.etc, "wifi.ssid")) == ssid;
		Error err = remove_all(wifi_control.saved_path(ssid));
		if (!err && last_chosen) err = wifi_control.forget_profile();
		if (!err) err = wifi_control.clear_selected_if(name);
		if (err) {
			finish_wifi(err);
			return fail(out, -1, "failed to forget the network");
		}
		if (associated != name) {
			wifi_control.remove_running_network(ssid);
			finish_wifi({});
			return ok(out);
		}
		ok(out);
		go_after(std::chrono::milliseconds(300), [] {
			Error e;
			// disable also drops the addresses of the left network.
			if (wifi_control.present() && !wifi_is_access_point()) {
				e = run_wifi_script("disable");
				if (!e) e = run_wifi_script("start");
			}
			finish_wifi(e);
		});
	});
}

// --- the access point and the country ---------------------------------------------------------------------------------

void get_wifi_ap(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		auto [ap, password_set] = read_wifi_ap();
		WifiAPState state;
		state.ap = ap;
		state.password_set = password_set;
		state.running = wifi_ap_active();
		state.address = wifi_ap_address(ap.subnet);
		state.device_dns = wifi_ap_device_dns();
		if (state.running) state.stations = wifi_ap_stations();
		if (wifi_control.present()) state.channels = wifi_control.ap_channels();
		std::string running;
		if (state.running) running = trim_space(read_file_or_empty(paths.wifi_ap_band_in_use));
		std::tie(state.band_in_use, state.band_fallback) = wifi_ap_band_fallback(ap.band, running, wifi_control.country(), state.channels);
		ok(out, wifi_ap_state_json(state));
	});
}

// saveWifiAPWithoutTrial: false when it has answered and released Wi-Fi.
bool save_wifi_ap_without_trial(http::Response &out, const WifiAP &ap, bool keep_password)
{
	auto err = without_trial([&] { return write_wifi_ap(ap, keep_password); });
	if (trial_blocking_error(err)) {
		finish_wifi({});
		fail(out, -3, err.text);
		return false;
	}
	if (err) {
		log::error("failed to write the Wi-Fi access point settings: {}", err.text);
		finish_wifi(err);
		fail(out, -2, "failed to save the access point settings");
		return false;
	}
	(void)sync_files();
	return true;
}

void set_wifi_ap(const Call &call, http::Response &res)
{
	auto decoded = decode_set_wifi_ap(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [ap = *decoded.value, o = call](http::Response &out) {
		auto [_, password_set] = read_wifi_ap();
		bool keep_password = ap.password.empty() && password_set;
		if (auto err = validate_wifi_ap(ap, keep_password)) return fail(out, -1, err.text);
		if (!wifi_control.hardware() || is_ap_mode()) return fail(out, -1, "Wi-Fi unavailable");
		// An adapter without 5 GHz would leave Wi-Fi down at every start. Turning such an access point off must stay
		// possible.
		if (ap.enabled && wifi_control.present()) {
			auto freqs = wifi_control.status_frequencies();
			if (freqs["2.4"].size() + freqs["5"].size() > 0 && freqs[ap.band].empty())
				return fail(out, -1, "this Wi-Fi adapter has no " + ap.band + " GHz band");
			if (auto err = allowed_wifi_ap_channel(ap, wifi_control.ap_channels())) return fail(out, -1, err.text);
		}
		if (!reserve_wifi()) return fail(out, -2, "Wi-Fi busy");
		// Only internet or DNS servers changed on the running access point: its clients stay associated.
		auto [old, unused] = read_wifi_ap();
		if (wifi_ap_active() && old.enabled && ap.enabled && old.ssid == ap.ssid && old.band == ap.band && old.channel == ap.channel &&
		    old.hidden == ap.hidden && old.subnet == ap.subnet && (keep_password || ap.password == read_wifi_ap_file("pass"))) {
			if (!save_wifi_ap_without_trial(out, ap, keep_password)) return;
			Error err = run_wifi_script("ap-internet");
			finish_wifi(err);
			if (err) {
				log::error("failed to apply the access point's internet setting: {}", err.text);
				return fail(out, -2, "failed to apply the internet setting");
			}
			ok(out, "{}");
			log::info("set the Wi-Fi access point's internet: {} dns=[{}]", ap.internet, join(ap.dns, " "));
			return;
		}
		// Over Wi-Fi, or a VPN that may run over it, turning the access point on or off can cut this page off: on
		// trial. Over Ethernet or USB the page stays, with nothing to confirm.
		if (!request_may_ride_wifi(o.local)) {
			if (!save_wifi_ap_without_trial(out, ap, keep_password)) return;
			ok(out, "{}");
			auto release = trial_work_begin();
			go_after(restart_delay, [release] {
				finish_wifi(restart_wifi_ap());
				release();
			});
			log::info("set the Wi-Fi access point: enabled={} band={} channel={} hidden={} internet={}", ap.enabled, ap.band,
			          ap.channel, ap.hidden, ap.internet);
			return;
		}
		Error err;
		auto trial = begin_trial("wifi-ap", wifi_ap_trial_files(), err);
		if (!trial) {
			finish_wifi({});
			return fail(out, -3, err.text);
		}
		if (auto e = write_wifi_ap(ap, keep_password)) {
			log::error("failed to write the Wi-Fi access point settings: {}", e.text);
			trial_abandon(trial);
			finish_wifi(e);
			return fail(out, -2, "failed to save the access point settings");
		}
		(void)sync_files();
		trial_start(trial);
		std::string address = ap.enabled ? wifi_ap_address(ap.subnet) : "";
		ok(out, "{\"trial\":" + trial_info_json(trial, trial_hosts(address, o.host)) + "}");
		auto release = trial_work_begin();
		go_after(restart_delay, [trial, release] {
			Error e = restart_wifi_ap();
			trial_restarted(trial, e);
			finish_wifi(e);
			release();
		});
		log::info("set the Wi-Fi access point on trial: enabled={} band={} channel={} hidden={} internet={}", ap.enabled, ap.band,
		          ap.channel, ap.hidden, ap.internet);
	});
}

void get_wifi_country(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		std::string country = wifi_control.country(), active = wifi_control.active_country();
		ok(out, wifi_country_json(country, active, supported_countries(), ""));
	});
}

void set_wifi_country(const Call &call, http::Response &res)
{
	auto decoded = decode_set_wifi_country(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [code = *decoded.value, o = call](http::Response &out) {
		// A country the database lacks would never come into force.
		if (!country_supported(code, supported_countries()))
			return fail(out, -1, "the regulatory database has no rules for the country " + code);
		if (!check_radio(out, false)) return;
		std::string previous = wifi_control.country();
		std::string active = wifi_control.active_country();
		bool restart = wifi_control.present() && wifi_control.enabled() && (code != previous || active != code);
		auto save_failed = [&](const Error &err) {
			log::error("failed to save the Wi-Fi country: {}", err.text);
			finish_wifi(err);
			fail(out, -2, "failed to save the Wi-Fi country");
		};
		if (!restart || !request_may_ride_wifi(o.local)) {
			auto err = without_trial([&] { return wifi_control.save_country(code); });
			if (trial_blocking_error(err)) {
				finish_wifi({});
				return fail(out, -3, err.text);
			}
			if (err) return save_failed(err);
			(void)sync_files();
			log::info("set the Wi-Fi country: {} (was {})", code, previous);
			if (!restart) {
				finish_wifi({});
				if (!wifi_control.present() || !wifi_control.enabled()) active = wifi_control.active_country();
				return ok(out, wifi_country_json(code, active, std::nullopt, ""));
			}
			// Active is still the previous domain; GET reads the new one.
			ok(out, wifi_country_json(code, active, std::nullopt, ""));
			auto release = trial_work_begin();
			go_after(restart_delay, [code, release] {
				finish_wifi(apply_wifi_country(code));
				release();
			});
			return;
		}
		Error err;
		auto trial = begin_trial("wifi-country", {wifi_control.country_path()}, err);
		if (!trial) {
			finish_wifi({});
			return fail(out, -3, err.text);
		}
		if (auto e = wifi_control.save_country(code)) {
			trial_abandon(trial);
			return save_failed(e);
		}
		(void)sync_files();
		trial_start(trial);
		// Acknowledge before the radio changes: a channel the new country does not allow drops the association
		// carrying this request at once.
		ok(out, wifi_country_json(code, active, std::nullopt, trial_info_json(trial, trial_hosts("", o.host))));
		auto release = trial_work_begin();
		go_after(restart_delay, [code, trial, release] {
			Error e = apply_wifi_country(code);
			trial_restarted(trial, e);
			finish_wifi(e);
			release();
		});
		log::info("set the Wi-Fi country on trial: {} (was {})", code, previous);
	});
}

// --- trials -------------------------------------------------------------------------------------------------------------

// Off the loop like every operation that takes the trial's lock: a confirmation holds it across a sync.
void get_probe_trial(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		out.set_header("Access-Control-Allow-Origin", "*");
		out.set_header("Cache-Control", "no-store");
		auto [id, applied] = probe_trial();
		ok(out, "{\"applied\":" + bool_json(applied) + ",\"id\":" + quote(id) + "}");
	});
}

void post_confirm_trial(const Call &call, http::Response &res)
{
	auto decoded = decode_confirm_trial(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [token = *decoded.value](http::Response &out) {
		bool no_trial = false;
		if (auto err = confirm_trial(token, no_trial)) return fail(out, no_trial ? -2 : -4, err.text);
		ok(out);
	});
}

// --- Ethernet -----------------------------------------------------------------------------------------------------------

void get_ethernet(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		EthernetConfig config;
		if (auto err = read_ethernet_config(config)) {
			log::error("failed to read ethernet config: {}", err.text);
			return fail(out, -1, "failed to read ethernet configuration");
		}
		ok(out, ethernet_config_json(config));
	});
}

void set_ethernet_forced(http::Response &out, const EthernetConfig &config, bool vlan)
{
	std::vector<std::string> files{paths.ethernet_config, paths.ethernet_disabled, paths.ethernet_vlan};
	auto err = without_trial([&]() -> Error {
		FileSnapshot previous;
		if (auto e = snapshot_files(files, previous)) return wrap("cannot record the current network settings", e);
		Error e = write_ethernet(config, vlan);
		if (!e) e = sync_files();
		if (e) {
			Error restore = restore_files(previous);
			if (!restore) restore = sync_files();
			if (restore) return Error{e.code, e.text + "; the previous settings could not be restored: " + restore.text};
			return e;
		}
		return {};
	});
	if (trial_blocking_error(err)) {
		release_ethernet_apply();
		return fail(out, -3, err.text);
	}
	if (err) {
		release_ethernet_apply();
		log::error("failed to write ethernet config: {}", err.text);
		return fail(out, -2, err.text);
	}
	ok(out, "{}");
	auto release = trial_work_begin();
	go_after(restart_delay, [release] {
		if (auto e = restart_ethernet()) log::error("failed to restart ethernet: {}", e.text);
		release_ethernet_apply();
		release();
	});
	log::info("set ethernet config without trial: enabled={} mode={} address={} gateway={}", config.enabled, config.mode, config.address,
	          config.gateway);
}

void set_ethernet(const Call &call, http::Response &res)
{
	auto decoded = decode_set_ethernet(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [r = *decoded.value, o = call](http::Response &out) {
		// One Ethernet change at a time, until its interface restart is over: S30eth's stop and start share the DHCP
		// and VLAN state.
		if (!reserve_ethernet_apply()) return fail(out, -3, ethernet_applying_text);
		if (r.force) return set_ethernet_forced(out, r.config, r.vlan);
		// The change can cut the device off: it runs on trial.
		Error err;
		auto trial = begin_trial("ethernet", {paths.ethernet_config, paths.ethernet_disabled, paths.ethernet_vlan}, err);
		if (!trial) {
			release_ethernet_apply();
			return fail(out, -3, err.text);
		}
		if (auto e = write_ethernet(r.config, r.vlan)) {
			log::error("failed to write ethernet config: {}", e.text);
			trial_abandon(trial);
			release_ethernet_apply();
			return fail(out, -2, e.text);
		}
		(void)sync_files();
		trial_start(trial);
		std::string address = r.config.enabled && r.config.mode == "static" ? r.config.address : "";
		ok(out, "{\"trial\":" + trial_info_json(trial, trial_hosts(address, o.host)) + "}");
		// A slow apply can outlive the trial's timeout and completed rollback: shutdown stays excluded until the
		// restart has returned.
		auto release = trial_work_begin();
		go_after(restart_delay, [trial, release] {
			trial_restarted(trial, restart_ethernet());
			release_ethernet_apply();
			release();
		});
		log::info("set ethernet config on trial: enabled={} mode={} address={} gateway={}", r.config.enabled, r.config.mode,
		          r.config.address, r.config.gateway);
	});
}

// --- DNS, IPv6, gateways ----------------------------------------------------------------------------------------------

void get_dns(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		std::string mode = current_dns_mode();
		auto servers = read_manual_dns_servers();
		if (servers.empty()) servers = parse_resolv_conf(paths.boot_resolv);
		if (servers.empty()) servers = parse_resolv_conf(paths.boot_resolv_backup);
		auto effective = parse_resolv_conf(paths.etc_resolv);
		ResolvConfig dhcp;
		(void)read_dhcp_resolv_config(can_fallback_effective_for_dhcp(), dhcp);
		ok(out, dns_json(mode, servers, effective, dhcp.servers, dns_info()));
	});
}

void set_dns(const Call &call, http::Response &res)
{
	auto decoded = decode_set_dns(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [value = *decoded.value](http::Response &out) {
		auto [mode, servers] = value;
		if (mode == "manual") {
			if (auto err = set_manual_dns(servers)) {
				log::error("failed to set manual dns: {}", err.text);
				return fail(out, -2, err.text);
			}
		} else if (auto err = set_dhcp_dns()) {
			log::error("failed to set dhcp dns: {}", err.text);
			return fail(out, -3, err.text);
		}
		(void)sync_files();
		ok(out);
	});
}

void get_ipv6_route(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		std::string json;
		auto [code, msg] = get_ipv6(json);
		if (code) return fail(out, code, msg);
		ok(out, json);
	});
}

void set_ipv6_route(const Call &call, http::Response &res)
{
	auto decoded = decode_set_ipv6(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [r = *decoded.value](http::Response &out) {
		auto [code, msg] = set_ipv6(r);
		if (code) return fail(out, code, msg);
		ok(out);
	});
}

void get_gateway_preference(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		ok(out, "{\"preferred\":" + quote(read_gateway_preference()) + ",\"routes\":" + gateway_routes_json(gateway_status_routes()) + "}");
	});
}

void set_gateway_preference(const Call &call, http::Response &res)
{
	auto decoded = decode_set_gateway_preference(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [preferred = *decoded.value](http::Response &out) {
		if (auto err = install_udhcpc_hook("99-nanokvm-gateway", udhcpc_gateway_hook)) return fail(out, -2, err.text);
		if (auto err = write_gateway_preference(preferred)) return fail(out, -2, err.text);
		Error err;
		{
			std::lock_guard lock(gateway_mu);
			err = apply_gateway_preference(preferred, default_gateway_entries());
		}
		if (err) return fail(out, -3, err.text);
		ok(out);
	});
}

void get_gateway_check(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		auto config = read_gateway_check();
		std::string gateway;
		std::optional<bool> reachable;
		bool demoted = false, carrier = false;
		for (const auto &route : gateway_status_routes()) {
			if (gateway_kind(route.interface) != "ethernet") continue;
			gateway = route.gateway;
			reachable = route.reachable;
			demoted = route.demoted;
			carrier = gateway_carrier(route.interface);
			break;
		}
		std::string json = "{\"enabled\":" + bool_json(config.enabled) + ",\"method\":" + quote(config.method) + ",\"host\":" + quote(config.host) +
		                   ",\"gateway\":" + quote(gateway) + ",\"carrier\":" + bool_json(carrier);
		if (reachable) json += ",\"reachable\":" + bool_json(*reachable);
		json += ",\"demoted\":" + bool_json(demoted) + ",\"wifi\":" + bool_json(is_supported()) + "}";
		ok(out, json);
	});
}

void set_gateway_check(const Call &call, http::Response &res)
{
	auto decoded = decode_set_gateway_check(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [config = *decoded.value](http::Response &out) {
		if (auto err = write_gateway_check(config)) return fail(out, -2, err.text);
		if (!config.enabled) gateway_watch.tick(config);
		ok(out);
	});
}

// --- Wake-on-LAN, peers, WebRTC -----------------------------------------------------------------------------------------

void wake_on_lan(const Call &call, http::Response &res)
{
	auto decoded = decode_wake(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	std::string user = call.user;
	defer_call(call, res, [user, value = *decoded.value](http::Response &out) {
		std::string message;
		int code = wake(user, value.first, value.second, message);
		if (code) return fail(out, code, message);
		ok(out);
	});
}

void get_mac(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		int code = 0;
		std::string msg;
		auto json = wol_macs_json(code, msg);
		if (code) return fail(out, code, msg);
		ok(out, json);
	});
}

void set_mac_name(const Call &call, http::Response &res)
{
	auto decoded = decode_set_mac_name(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [value = *decoded.value](http::Response &out) {
		auto [code, msg] = set_wol_mac_name(value.first, value.second);
		if (code) return fail(out, code, msg);
		ok(out);
	});
}

void delete_mac(const Call &call, http::Response &res)
{
	auto decoded = decode_delete_mac(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [mac = *decoded.value](http::Response &out) {
		auto [code, msg] = delete_wol_mac(mac);
		if (code) return fail(out, code, msg);
		ok(out);
	});
}

void get_wol_interfaces(const Call &call, http::Response &res)
{
	defer_call(call, res, [](http::Response &out) {
		Error err;
		auto list = wol_interfaces(&err);
		if (!list) {
			log::error("failed to list wake-on-lan interfaces: {}", err.text);
			return fail(out, -2, "list interfaces failed");
		}
		ok(out, "{\"interfaces\":" + json_strings(*list, false) + "}");
	});
}

void get_peers(http::Request &req, http::Response &res)
{
	// About three seconds of listening: a thread of its own, as a goroutine, not a slot of the blocking pool.
	res.pending = true;
	std::thread([id = req.id, headers = res.headers] {
		auto peers = browse_peers(std::chrono::steady_clock::now() + std::chrono::seconds(3));
		std::stable_sort(peers.begin(), peers.end(), [](const Peer &a, const Peer &b) { return a.name < b.name; });
		http::Response out;
		out.headers = headers;
		out.set_header("Cache-Control", "no-store");
		ok(out, "{\"peers\":" + peers_json(peers) + "}");
		http::complete(id, std::move(out));
	}).detach();
}

void get_webrtc_servers(const Call &call, http::Response &res)
{
	// The settings' lock is held across the write of server.yaml.
	defer_call(call, res, [](http::Response &out) { ok(out, webrtc_servers_state_json(read_webrtc())); });
}

void post_webrtc_servers(const Call &call, http::Response &res)
{
	auto decoded = decode_set_webrtc_servers(call.data);
	if (!decoded.value) return refuse(res, decoded.refusal);
	defer_call(call, res, [r = *decoded.value](http::Response &out) {
		std::string state;
		auto [code, msg] = set_webrtc_servers(r, state);
		if (code) return fail(out, code, msg);
		ok(out, state);
	});
}

} // namespace

// --- request decoding of the Wi-Fi and trial routes ---------------------------------------------------------------------

Decoded<std::pair<std::string, std::string>> decode_connect_wifi_no_auth(const RequestData &data)
{
	Decoded<std::pair<std::string, std::string>> d;
	auto b = Binder::bind(data, false);
	std::string ssid, password;
	if (b.ok()) {
		b.str("Ssid", "Ssid", ssid);
		b.str("Password", "Password", password);
	}
	if (!b.ok() || ssid.empty() || password.empty()) {
		d.refusal = {-2, "invalid parameters"};
		return d;
	}
	d.value = std::pair{ssid, password};
	return d;
}

Decoded<bool> decode_set_wifi_enabled(const RequestData &data)
{
	Decoded<bool> d;
	auto b = Binder::bind(data, true);
	bool enabled = false;
	bool has = b.ok() && b.boolean("enabled", "", enabled);
	if (!b.ok() || !has) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = enabled;
	return d;
}

Decoded<std::string> decode_set_wifi_band_preference(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, true);
	std::string band;
	if (b.ok()) b.str("preferredBand", "", band);
	if (!b.ok() || (band != "2.4" && band != "5")) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = band;
	return d;
}

Decoded<WifiProfile> decode_configure_wifi(const RequestData &data)
{
	Decoded<WifiProfile> d;
	auto b = Binder::bind(data, true);
	WifiProfile p;
	if (b.ok()) {
		b.str("ssid", "", p.ssid);
		b.str("password", "", p.password);
		b.str("band", "", p.band);
		b.str("preferredBand", "", p.preferred_band);
		b.boolean("hidden", "", p.hidden);
		b.str("security", "", p.security);
		bool auto_connect = false;
		if (b.boolean("autoConnect", "", auto_connect)) p.auto_connect = auto_connect;
	}
	if (!b.ok() || p.validate()) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = p;
	return d;
}

Decoded<std::string> decode_saved_wifi(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, true);
	std::string ssid;
	if (b.ok()) b.str("ssid", "", ssid);
	if (!b.ok() || ssid.empty()) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = ssid;
	return d;
}

Decoded<std::string> decode_set_wifi_country(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, true);
	std::string country;
	if (b.ok()) b.str("country", "", country);
	if (!b.ok() || !valid_wifi_country(country)) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = country;
	return d;
}

Decoded<WifiAP> decode_set_wifi_ap(const RequestData &data)
{
	Decoded<WifiAP> d;
	auto b = Binder::bind(data, true);
	WifiAP ap;
	if (b.ok()) {
		b.boolean("enabled", "", ap.enabled);
		b.str("ssid", "", ap.ssid);
		b.str("password", "", ap.password);
		b.str("band", "", ap.band);
		b.integer("channel", "", ap.channel);
		b.boolean("hidden", "", ap.hidden);
		b.str("subnet", "", ap.subnet);
		b.boolean("internet", "", ap.internet);
		if (b.strings("dns", "", ap.dns)) ap.dns_nil = false;
	}
	if (!b.ok()) {
		d.refusal = {-1, "invalid parameters"};
		return d;
	}
	d.value = ap;
	return d;
}

Decoded<std::string> decode_confirm_trial(const RequestData &data)
{
	Decoded<std::string> d;
	auto b = Binder::bind(data, true);
	std::string token;
	if (b.ok()) b.str("token", "", token);
	if (!b.ok() || token.empty()) {
		d.refusal = {-1, "invalid arguments"};
		return d;
	}
	d.value = token;
	return d;
}

// The handler of an operation: the request read on the loop, the operation run as it decides (defer_call).
http::Handler handler(void (*op)(const Call &, http::Response &))
{
	return [op](http::Request &req, http::Response &res) {
		op(Call{req.id, request_data(req), local_address(req), req.header("host"), req.auth ? req.auth->username : ""}, res);
	};
}

void add_routes(http::Router &router)
{
	auth::Guard admin{{"admin"}};
	// Without a login: the Wi-Fi setup in AP mode, and a network change on trial found and kept from a page on any
	// address, which may have no session there yet.
	router.add("POST", "/api/network/wifi", connect_wifi_no_auth);
	router.add("POST", "/api/network/wifi/verify", [](http::Request &req, http::Response &res) {
		if (check_ap_request(req, res)) ok(res);
	});
	router.add("GET", "/api/network/trial/probe", handler(get_probe_trial));
	router.add("POST", "/api/network/trial/confirm", handler(post_confirm_trial));

	router.add("POST", "/api/network/wol", auth::guarded(handler(wake_on_lan)));
	router.add("GET", "/api/network/wol/mac", auth::guarded(handler(get_mac)));
	router.add("GET", "/api/network/wol/interfaces", auth::guarded(handler(get_wol_interfaces)));
	router.add("GET", "/api/network/peers", auth::guarded(get_peers));

	router.add("DELETE", "/api/network/wol/mac", auth::guarded(handler(delete_mac), admin));
	router.add("POST", "/api/network/wol/mac/name", auth::guarded(handler(set_mac_name), admin));
	router.add("GET", "/api/network/wifi/scan", auth::guarded(handler(scan_wifi), admin));
	router.add("POST", "/api/network/wifi/enabled", auth::guarded(handler(set_wifi_enabled), admin));
	router.add("POST", "/api/network/wifi/band-preference", auth::guarded(handler(set_wifi_band_preference), admin));
	router.add("GET", "/api/network/wifi/country", auth::guarded(handler(get_wifi_country), admin));
	router.add("POST", "/api/network/wifi/country", auth::guarded(handler(set_wifi_country), admin));
	router.add("POST", "/api/network/wifi/configure", auth::guarded(handler(configure_wifi), admin));
	router.add("GET", "/api/network/wifi", auth::guarded(handler(get_wifi), admin));
	router.add("POST", "/api/network/wifi/disconnect", auth::guarded(handler(disconnect_wifi), admin));
	router.add("GET", "/api/network/wifi/ap", auth::guarded(handler(get_wifi_ap), admin));
	router.add("POST", "/api/network/wifi/ap", auth::guarded(handler(set_wifi_ap), admin));
	router.add("GET", "/api/network/wifi/saved", auth::guarded(handler(get_saved_wifi), admin));
	router.add("POST", "/api/network/wifi/saved/connect", auth::guarded(handler(connect_saved_wifi), admin));
	router.add("POST", "/api/network/wifi/saved/forget", auth::guarded(handler(forget_saved_wifi), admin));
	router.add("GET", "/api/network/ipv6", auth::guarded(handler(get_ipv6_route), admin));
	router.add("POST", "/api/network/ipv6", auth::guarded(handler(set_ipv6_route), admin));
	router.add("GET", "/api/network/dns", auth::guarded(handler(get_dns), admin));
	router.add("POST", "/api/network/dns", auth::guarded(handler(set_dns), admin));
	router.add("GET", "/api/network/ethernet", auth::guarded(handler(get_ethernet), admin));
	router.add("POST", "/api/network/ethernet", auth::guarded(handler(set_ethernet), admin));
	router.add("GET", "/api/network/gateway", auth::guarded(handler(get_gateway_preference), admin));
	router.add("POST", "/api/network/gateway", auth::guarded(handler(set_gateway_preference), admin));
	router.add("GET", "/api/network/ethernet/gateway-check", auth::guarded(handler(get_gateway_check), admin));
	router.add("POST", "/api/network/ethernet/gateway-check", auth::guarded(handler(set_gateway_check), admin));
	router.add("GET", "/api/network/webrtc", auth::guarded(handler(get_webrtc_servers), admin));
	router.add("POST", "/api/network/webrtc", auth::guarded(handler(post_webrtc_servers), admin));
}

bool initialize_ipv6() { return initialize_ipv6_logged(); }

bool ensure_dhcp_integration()
{
	if (auto err = ensure_dhcp_integration_error()) {
		log::error("failed to set up the DHCP DNS and gateway hooks: {}", err.text);
		return false;
	}
	return true;
}

} // namespace nkos::network
