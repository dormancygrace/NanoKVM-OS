#pragma once

// The network family's parts, shared between its files: the Go server's service/network, function by function.
// Paths and the commands it runs are variables, as in the Go code, so that the differential check can point them at
// a temporary tree and record the commands.

#include "network/util.hpp"

#include <chrono>
#include <cstdint>
#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <set>
#include <string>
#include <vector>

namespace nkos::network {

// The files and directories the family reads and writes.
struct Paths {
	// wifi.go
	std::string ap_mode_file = "/run/nanokvm-wifiap";
	std::string wifi_ssid = "/etc/kvm/wifi.ssid";
	std::string wifi_pass = "/etc/kvm/wifi.pass";
	std::string wifi_connect = "/kvmapp/kvm/wifi_try_connect";
	std::string ap_pass_file = "/kvmapp/kvm/ap.pass";
	std::string wlan0 = "/sys/class/net/wlan0";
	std::string etc_kvm = "/etc/kvm";
	// wifi_ap.go
	std::string wifi_ap_prefix = "/etc/kvm/wifi.ap";
	std::string wifi_ap_running = "/run/nanokvm-wifi/ap-user";
	std::string wifi_ap_band_in_use = "/run/nanokvm-wifi/ap-band";
	std::string wifi_ap_leases = "/run/nanokvm-wifi/udhcpd.leases";
	std::string wifi_ap_resolv_conf = "/etc/resolv.conf";
	std::string wifi_ap_uid_file = "/sys/class/cvi-base/base_uid";
	// wifi_country.go
	std::string regulatory_db = "/lib/firmware/regulatory.db";
	// trial.go
	std::string trial_file = "/boot/net-trial.json";
	std::string avahi_pid = "/run/avahi-daemon/pid";
	std::string etc_hostname = "/etc/hostname";
	// ethernet.go
	std::string ethernet_config = "/boot/eth.nodhcp";
	std::string ethernet_disabled = "/boot/eth.disabled";
	std::string ethernet_carrier = "/sys/class/net/eth0/carrier";
	std::string ethernet_flags = "/sys/class/net/eth0/flags";
	std::string ethernet_vlan = "/boot/eth.vlan";
	// dns.go
	std::string dns_config_dir = "/etc/kvm/network";
	std::string dns_mode_file = "/etc/kvm/network/dns.mode";
	std::string dns_servers_file = "/etc/kvm/network/dns.servers";
	std::string boot_resolv = "/boot/resolv.conf";
	std::string boot_resolv_backup = "/boot/resolv.conf.manual.bak";
	std::string dhcp_resolv = "/etc/resolv.conf.dhcp";
	std::string etc_resolv = "/etc/resolv.conf";
	std::string proc_net_route = "/proc/net/route";
	// udhcpc_hooks.go
	std::vector<std::string> udhcpc_hook_dirs = {"/etc/udhcpc/post-bound", "/etc/udhcpc/post-renew"};
	std::string udhcpc_deconfig_dir = "/etc/udhcpc/post-deconfig";
	std::string legacy_hook_dir = "/usr/share/udhcpc/default.script.d";
	std::string udhcpc_conf = "/etc/udhcpc/udhcpc.conf";
	// ipv6.go
	std::string ipv6_config = "/etc/kvm/network/ipv6";
	std::string ipv6_sysctl_root = "/proc/sys/net/ipv6/conf";
	std::string ipv6_run_dir = "/run";
	std::string ipv6_boot_script = "/etc/init.d/S02ipv6";
	std::string temp_dir = "/tmp";
	// gateway.go, gateway_check.go
	std::string gateway_preference = "/etc/kvm/network/gateway.preferred";
	std::string gateway_route_table = "/proc/net/route";
	std::string gateway_check_file = "/etc/kvm/network/gateway.check";
	std::string gateway_demoted_dir = "/run/nanokvm/gateway-demoted";
	std::string gateway_sys_net = "/sys/class/net";
	std::string gateway_proc_dir = "/proc";
	std::string gateway_dhcp_pid_file = "/run/udhcpc.eth0.pid";
	// wol.go
	std::string wol_mac_file = "/etc/kvm/cache/wol";
	std::string wol_sys_class_net = "/sys/class/net";
	// discovery.go
	std::string discovery_file = "/etc/avahi/services/nanokvm.service";
	// config/webrtc.go: the server configuration the WebRTC servers are kept in
	std::string server_yaml = "/etc/kvm/server.yaml";
};
extern Paths paths;

constexpr const char *wifi_script = "/etc/init.d/S30wifi";
constexpr const char *ethernet_init_script = "/etc/init.d/S30eth";

// The embedded udhcpc hooks and boot script (scripts/ of the Go package).
extern const std::string_view udhcpc_dns_hook, udhcpc_gateway_hook, udhcpc_wifi_source_hook, ipv6_boot_script_text;

// The delays before a change replaces the interface its reply travels on.
extern std::chrono::milliseconds restart_delay;  // 1 s

// --- Wi-Fi (wifi.go, wifi_control.go, wifi_saved.go, wpa_ctrl.go) ------------------------------------------------------

using Freqs = std::map<std::string, std::vector<std::string>>;
using Channels = std::map<std::string, std::vector<int>>;

struct BandCache {
	std::mutex mu;
	std::chrono::nanoseconds ttl{std::chrono::seconds(30)};
	std::string phy;
	std::chrono::steady_clock::time_point at;
	std::optional<Freqs> freqs;
	Freqs get(const std::string &phy, std::chrono::steady_clock::time_point now, const std::function<Freqs()> &read);
	void reset();
};

struct ApChannelCache {
	std::mutex mu;
	std::chrono::nanoseconds ttl{std::chrono::seconds(30)};
	std::string phy;
	std::chrono::steady_clock::time_point at;
	bool cached = false;
	std::optional<Channels> channels;
	void reset();
};

struct WifiProfile {
	std::string ssid, password, band, preferred_band, security;
	bool hidden = false;
	std::optional<bool> auto_connect;
	bool auto_connect_or_default() const { return !auto_connect || *auto_connect; }
	Error validate() const;
};
WifiProfile legacy_profile(const std::string &ssid, const std::string &password);

struct WifiNetwork {
	std::string bssid, ssid, band;
	double signal = -100;
	std::string security;
};

struct SavedNetwork {
	std::string ssid, security;
	bool hidden = false, auto_connect = false, current = false;
	std::int64_t used_ns = 0;
};

struct WifiConnection {
	int frequency = 0, channel = 0, signal = 0, link_speed = 0, generation = 0;
	std::string width, key_mgmt, ip;
};

struct WifiStatus {
	bool enabled = false;
	std::string model;
	std::vector<std::string> bands;
	std::string band;
	bool busy = false;
	std::string error;
	bool supported = false, ap_mode = false, access_point = false;
	int ap_stations = 0;
	bool connected = false;
	std::string ssid, preferred_band;
	std::optional<WifiConnection> connection;
	bool this_connection = false;
};
std::string wifi_status_json(const WifiStatus &s);

struct RadioControl {
	std::string etc, net, sdio, run_dir;
	// run: a command (name, args) and its standard output; request: a wpa_supplicant control command, nullopt on an
	// error. An empty request is no control socket.
	std::function<RunResult(const std::string &, const std::vector<std::string> &)> run;
	std::function<std::optional<std::string>(const std::string &)> request;
	BandCache *bands = nullptr;
	ApChannelCache *ap_bands = nullptr;

	bool present() const;
	std::string chip_alias() const;
	bool hardware() const;
	bool enabled() const;
	std::string model() const;
	std::string phy() const;
	Freqs phy_frequencies(const std::string &phy) const;
	Freqs frequencies() const;
	Freqs status_frequencies() const;
	std::string preferred_band() const;
	Error save_preferred_band(const std::string &band) const;
	std::vector<std::string> band_frequencies(const std::string &band) const;
	Error apply_preferred_band(const std::string &band) const;
	WifiStatus status() const;
	Error save_profile(const WifiProfile &p, const Freqs &frequencies, const std::string &preferred_band) const;
	Error forget_profile() const;
	// wifi_saved.go
	std::string saved_path(const std::string &ssid) const;
	std::string selected_path() const;
	Error save_network(const WifiProfile &p, bool auto_connect) const;
	Error trim_saved(const std::string &keep) const;
	std::vector<SavedNetwork> read_saved(const std::string &current, Error *err) const;
	bool saved_profile(const std::string &ssid, WifiProfile &p, bool &auto_connect, Error &err) const;
	Error migrate_saved() const;
	std::optional<WifiProfile> configured_profile() const;
	Error set_auto_connect(const std::string &ssid, bool auto_connect) const;
	Error select_network(const std::string &ssid) const;
	Error clear_selected() const;
	Error clear_selected_if(const std::string &name) const;
	std::optional<std::string> associated() const;
	std::optional<std::string> running_name(const std::string &id, const std::string &listed) const;
	void remove_running_network(const std::string &ssid) const;
	Error stop_joining_associated() const;
	Error remember_joined(const WifiProfile &p, bool auto_connect) const;
	// wifi_country.go
	std::string country_path() const;
	std::string country() const;
	Error save_country(const std::string &code) const;
	std::string active_country() const;
	std::optional<Channels> ap_channels() const;
	void country_changed() const;
};
extern RadioControl wifi_control;

std::string band_for(int freq);
int channel_for(int freq);
Freqs parse_frequencies(const std::string &output);
std::string decode_ssid(const std::string &value);
std::vector<WifiNetwork> parse_scan(const std::string &output, const std::string &band);
std::string wifi_networks_json(const std::vector<WifiNetwork> &list);
std::string saved_networks_json(const std::vector<SavedNetwork> &list);
std::string saved_name(const std::string &ssid);
bool valid_saved_name(const std::string &name);
std::optional<std::string> wpa_unescape(const std::string &value);
std::string read_trimmed(const std::string &path);
Error write_private(const std::string &path, std::string_view data);

bool reserve_wifi();
void finish_wifi(const Error &err);
bool is_supported();
bool is_ap_mode();
std::string ap_password();

// The setup access point's key guard (wifi.go apKeyGuard).
struct ApKeyGuard {
	std::mutex mu;
	std::string key;
	int failures = 0;
	// ok, locked
	std::pair<bool, bool> allow(const std::string &given, const std::string &current);
};
extern ApKeyGuard ap_guard;
inline constexpr int ap_key_max_failures = 10;

// wpa_ctrl.go: a client for wpa_supplicant's control socket.
std::function<std::optional<std::string>(const std::string &)> wpa_control(const std::string &socket);

// Whether a Wi-Fi change can cut off the request whose connection ends at local (requestMayRideWifi).
bool request_may_ride_wifi(const std::optional<IP16> &local);

// --- the administrator's access point (wifi_ap.go) -------------------------------------------------------------------

struct WifiAP {
	bool enabled = false;
	std::string ssid, password, band;
	long long channel = 0;
	bool hidden = false;
	std::string subnet;
	bool internet = false;
	std::vector<std::string> dns;  // nil when empty
	bool dns_nil = true;
};
struct WifiAPStation {
	std::string mac, ip, hostname;
	int signal = 0, connected = 0;
	double rate = 0;
};
struct WifiAPState {
	WifiAP ap;
	bool password_set = false, running = false;
	std::string address;
	std::vector<WifiAPStation> stations;
	std::vector<std::string> device_dns;
	std::optional<Channels> channels;
	std::string band_in_use, band_fallback;
};
std::string wifi_ap_state_json(const WifiAPState &s);
std::string channels_json(const Channels &c);

std::string wifi_ap_file(const std::string &name);
std::vector<std::string> wifi_ap_files();
std::vector<std::string> wifi_ap_trial_files();
bool valid_wifi_ap_channel(const std::string &band, long long channel);
std::string read_file_or_empty(const std::string &path);
std::string read_wifi_ap_file(const std::string &name);
std::pair<WifiAP, bool> read_wifi_ap();
bool wifi_ap_active();
bool wifi_is_access_point();
bool valid_wifi_ap_password(const std::string &password);
Error valid_wifi_ap_subnet(const std::string &subnet);
Error allowed_wifi_ap_channel(const WifiAP &ap, const std::optional<Channels> &allowed);
std::pair<std::string, std::string> wifi_ap_band_fallback(const std::string &band, const std::string &running,
                                                          const std::string &country, const std::optional<Channels> &allowed);
Error validate_wifi_ap(const WifiAP &ap, bool keep_password);
Error write_wifi_ap(const WifiAP &ap, bool keep_password);
std::string wifi_ap_address(const std::string &subnet);
std::vector<WifiAPStation> wifi_ap_stations();
std::vector<std::string> wifi_ap_device_dns();
std::vector<WifiAPStation> parse_all_sta(const std::string &out);
std::map<std::string, std::pair<std::string, std::string>> parse_dump_leases(const std::string &out);
Error restart_wifi_ap();

// --- the Wi-Fi country (wifi_country.go) ----------------------------------------------------------------------------

bool valid_wifi_country(const std::string &code);
// nullopt with err set when it is not a regulatory database of version 20.
std::optional<std::vector<std::string>> parse_regulatory_db(std::string_view data, std::string &err);
std::optional<std::vector<std::string>> supported_countries();
bool country_supported(const std::string &code, const std::optional<std::vector<std::string>> &supported);
std::string parse_reg_country(const std::string &output);
std::optional<Channels> parse_ap_channels(const std::string &output);
Error apply_wifi_country(const std::string &code);
Error restart_wifi_country();
std::string wifi_country_json(const std::string &country, const std::string &active,
                              const std::optional<std::vector<std::string>> &supported, const std::string &trial_json);

// --- network changes on trial (trial.go) ----------------------------------------------------------------------------

using FileSnapshot = std::map<std::string, std::optional<std::string>>;
struct TrialRecord {
	FileSnapshot files;
	bool files_nil = false;
	std::string service;
};
std::string trial_record_json(const TrialRecord &record);
// RestorePendingTrial's reading of a record: nullopt with why when it is unusable.
std::optional<TrialRecord> parse_trial_record(const std::string &data, std::string &why);
bool trial_path_allowed(const std::string &path);
Error snapshot_files(const std::vector<std::string> &list, FileSnapshot &out);
Error restore_files(const FileSnapshot &files);

struct Trial;
// The trial a change runs on, held by the change's background work.
using TrialPtr = std::shared_ptr<Trial>;
extern const char *trial_pending_text, *trial_undoing_text;
// begin_trial: nullptr with err (its text is the reply) when another change is on trial or being undone, or the
// record cannot be written.
TrialPtr begin_trial(const std::string &service, const std::vector<std::string> &files, Error &err);
void trial_start(const TrialPtr &t);
void trial_restarted(const TrialPtr &t, const Error &err);
void trial_abandon(const TrialPtr &t);
std::string trial_info_json(const TrialPtr &t, const std::vector<std::string> &hosts);
// withoutTrial: runs write unless a change is on trial or being undone (then the error says so).
Error without_trial(const std::function<Error()> &write);
bool trial_blocking_error(const Error &err);  // errTrialPending or errTrialUndoing
Error confirm_trial(const std::string &token, bool &no_trial);
std::pair<std::string, bool> probe_trial();
extern std::function<std::vector<std::string>()> interface_addrs;  // the device's addresses, "ip/len"
extern std::function<std::string()> mdns_host;
std::vector<std::string> trial_hosts(const std::string &new_address, const std::string &request_host);
// networktrial.Begin: keeps shutdown and standby waiting; the returned function releases (once).
std::function<void()> trial_work_begin();

// --- Ethernet (ethernet.go, ethernet_vlan.go) -----------------------------------------------------------------------

struct EthernetConfig {
	bool enabled = false, admin_up = false, link_up = false;
	std::string mode, interface, address, subnet_mask, gateway;
	bool vlan_enabled = false;
	long long vlan_id = 0;
	std::string current_address;
};
std::string ethernet_config_json(const EthernetConfig &c);
struct StaticEthernet {
	Prefix prefix;
	Addr gateway;
};
Error validate_static_ethernet(const std::string &address, const std::string &subnet_mask, const std::string &gateway,
                               StaticEthernet &out);
std::optional<int> subnet_mask_to_prefix(const std::string &mask, Error &err);
std::string prefix_to_subnet_mask(int prefix);
Addr ipv4_broadcast(const Prefix &prefix);
Error read_ethernet_config(EthernetConfig &config);
Error read_ethernet_enabled(bool &enabled);
bool read_ethernet_carrier();
bool read_ethernet_admin_up();
Error read_ethernet_vlan(bool &enabled, long long &id);
Error write_ethernet_vlan(bool enabled, long long id);
Error write_ethernet_enabled(bool enabled);
Error write_ethernet_config(const EthernetConfig &config);
Error write_ethernet(const EthernetConfig &config, bool vlan);
extern std::function<std::string(const std::string &)> ethernet_ipv4;
extern std::function<Error()> restart_ethernet;

// --- DNS (dns.go, udhcpc_hooks.go) ----------------------------------------------------------------------------------

struct ResolvConfig {
	std::vector<std::string> servers, search_domains;
};
Error parse_resolv_config(const std::string &path, ResolvConfig &out);
std::vector<std::string> parse_resolv_conf(const std::string &path);
std::optional<std::vector<std::string>> normalize_dns_servers(const std::vector<std::string> &servers, Error &err);
std::optional<std::vector<std::string>> validate_dns_servers(const std::vector<std::string> &servers, Error &err);
std::vector<std::string> normalize_search_domains(const std::vector<std::string> &domains);
std::string normalize_dns_server(const std::string &server);
std::string strip_inline_comment(const std::string &value);
Error render_resolv_config(const std::string &path, const ResolvConfig &config);
std::string current_dns_mode();
std::string read_raw_dns_mode();
bool can_fallback_effective_for_dhcp();
std::vector<std::string> read_manual_dns_servers();
Error read_dhcp_resolv_config(bool allow_effective_fallback, ResolvConfig &out);
Error refresh_dhcp_resolv_cache_from_effective();
Error set_manual_dns(const std::vector<std::string> &servers);
Error set_dhcp_dns();
std::string parse_route_gateway(const std::string &value);
std::string dns_interface_type(const std::string &name);
struct DNSInfo {
	std::string interface, type, address, subnet_mask, gateway;
	std::vector<std::string> search_domains;
};
DNSInfo dns_info();
std::string dns_json(const std::string &mode, const std::vector<std::string> &servers, const std::vector<std::string> &effective,
                     const std::vector<std::string> &dhcp, const DNSInfo &info);
extern const char *udhcpc_resolv_line;
Error install_udhcpc_hook_in(const std::vector<std::string> &dirs, const std::string &name, std::string_view script);
Error install_udhcpc_hook(const std::string &name, std::string_view script);
Error ensure_udhcpc_leaves_resolv_conf();
extern std::function<Error(const std::string &tool, const std::string &input, const std::vector<std::string> &args)> resolvconf_runner;
Error publish_base_resolver(const std::string &tool, const std::string &content);
Error adopt_base_resolver(const std::string &tool, std::string base);
Error adopt_resolv_conf();
Error clear_dhcpv6_resolvers();
Error ensure_dhcp_integration_error();

// --- IPv6 (ipv6.go) -------------------------------------------------------------------------------------------------

struct IPv6Setting {
	std::string mode, address, gateway;
	Error normalized(IPv6Setting &out) const;
};
using IPv6Policy = std::map<std::string, IPv6Setting>;
extern const std::vector<std::string> ipv6_interfaces;
std::optional<IPv6Policy> parse_ipv6_policy(const std::string &text);
bool ipv6_policy_any(const IPv6Policy &p);
std::vector<std::string> ipv6_policy_words(const IPv6Policy &p);
Error read_ipv6_policy(IPv6Policy &out);
Error write_ipv6_policy(const IPv6Policy &p);
Error apply_ipv6(const IPv6Policy &p);
// done, err
std::pair<bool, Error> apply_ipv6_off();
Error initialize_ipv6_policy();
std::string ipv6_source(const std::string &iface, const Addr &ip);
bool initialize_ipv6_logged();
Error ipv6_status_json(std::string &out);

// --- gateways (gateway.go, gateway_check.go) ------------------------------------------------------------------------

struct GatewayRoute {
	std::string interface, gateway;
	int metric = 0;
	std::optional<bool> reachable;
	bool demoted = false;
};
std::string gateway_route_json(const GatewayRoute &r);
std::string gateway_routes_json(const std::vector<GatewayRoute> &routes);
extern std::function<RunResult(const std::vector<std::string> &)> gateway_route_command;
extern std::function<std::set<int>()> occupied_metrics;
std::string read_gateway_preference();
Error write_gateway_preference(const std::string &preferred);
std::vector<GatewayRoute> parse_default_gateway_entries(const std::string &table);
std::vector<GatewayRoute> default_gateway_entries();
std::vector<GatewayRoute> unique_gateway_routes(const std::vector<GatewayRoute> &entries);
std::string gateway_kind(const std::string &iface);
int preference_metric(const std::string &preferred, const std::string &iface, int index);
Error apply_gateway_preference(const std::string &preferred, const std::vector<GatewayRoute> &entries);
extern std::mutex gateway_mu;

struct GatewayCheckConfig {
	bool enabled = false;
	std::string method = "arp", host;
};
bool valid_gateway_check(const GatewayCheckConfig &c);
GatewayCheckConfig read_gateway_check();
Error write_gateway_check(const GatewayCheckConfig &c);
std::string gateway_check_json(const GatewayCheckConfig &c);
extern std::function<bool(const std::string &iface, const std::string &gateway, const std::string &method)> probe_gateway;
extern std::function<bool(const std::string &iface, const std::string &host)> probe_host;
extern std::function<bool(const std::string &iface)> probe_dhcp_server;
extern std::function<Error(int pid)> restart_gateway_dhcp;
bool gateway_carrier(const std::string &iface);
int gateway_dhcp_client(const std::string &iface);
std::optional<int> demoted_original(const std::string &iface);
Error write_demoted_original(const std::string &iface, int metric);
std::pair<int, Error> move_gateway_route(const GatewayRoute &route, const std::vector<GatewayRoute> &entries, int from, int limit);
Error demote_gateway(const GatewayRoute &route, const std::vector<GatewayRoute> &entries);
Error restore_gateway(const GatewayRoute &route, const std::vector<GatewayRoute> &entries);
void restore_all_gateways();
struct GatewayHealth {
	int passes = 0, fails = 0;
	std::optional<std::chrono::steady_clock::time_point> silent_since;
	std::optional<std::chrono::steady_clock::time_point> last_dhcp;
	bool checked = false, reachable = false, kept_lease = false;
};
struct GatewayChecker {
	std::mutex mu;
	std::map<std::string, std::shared_ptr<GatewayHealth>> health;
	std::function<std::chrono::steady_clock::time_point()> now = [] { return std::chrono::steady_clock::now(); };
	void tick(const GatewayCheckConfig &config);
	std::pair<bool, bool> status(const std::string &iface);
};
extern GatewayChecker gateway_watch;
std::vector<GatewayRoute> gateway_status_routes();

// --- Wake-on-LAN (wol.go) -------------------------------------------------------------------------------------------

struct WakeLimiter {
	std::mutex mu;
	double burst;
	std::chrono::nanoseconds every;
	double tokens = 0;
	std::optional<std::chrono::steady_clock::time_point> last;
	WakeLimiter(double b, std::chrono::nanoseconds e) : burst(b), every(e) {}
	bool allow(std::chrono::steady_clock::time_point now);
};
extern std::function<std::vector<Interface>(bool *)> system_network_interfaces;
std::optional<std::string> parse_mac(const std::string &mac);
bool split_wol_mac_line(const std::string &line, std::string &mac, std::string &name);
std::string format_wol_mac_line(const std::string &mac, const std::string &name);
std::string sanitize_wol_mac_name(const std::string &name);
std::vector<std::string> bound_wol_history(std::vector<std::string> macs);
std::optional<std::vector<std::string>> read_wol_macs(Error *err);
Error write_wol_macs(const std::vector<std::string> &macs);
std::optional<std::vector<std::string>> wol_interfaces(Error *err);
std::optional<std::string> select_wol_interface(const std::string &requested);
// The rename and delete of the history (SetMacName, DeleteMac): code and message, 0 on success.
std::pair<int, std::string> set_wol_mac_name(const std::string &mac, const std::string &name);
std::pair<int, std::string> delete_wol_mac(const std::string &mac);
std::string wol_macs_json(int &code, std::string &msg);

// --- discovery (discovery.go) ---------------------------------------------------------------------------------------

struct Peer {
	std::string name, host;
	std::vector<std::string> addresses;
	int port = 0;
	std::string proto, model;
	bool self = false;
};
std::string peers_json(const std::vector<Peer> &peers);
std::string discovery_service(const std::string &proto, int http_port, int https_port, const std::string &model);
// A DNS message's question for name and type (mDNS, unicast answer asked); empty when the name cannot be packed.
std::string discovery_query(const std::string &name, std::uint16_t type);
struct SRV {
	std::uint16_t priority = 0, weight = 0, port = 0;
	std::string target;
};
struct DiscoveryAnswers {
	std::set<std::string> instances;
	std::map<std::string, SRV> targets;
	std::map<std::string, std::vector<std::string>> texts;
	std::map<std::string, std::vector<std::string>> addresses;
	void add(std::string_view message);
	std::vector<std::string> missing() const;
	std::vector<Peer> peers() const;
};
struct LinkPeers {
	std::vector<Peer> peers;
	std::vector<std::string> local;
};
std::vector<Peer> collect_peers(const std::vector<LinkPeers> &links);
std::string discovery_instance_name(const std::string &instance);
void publish_discovery_file(const std::string &want);
std::vector<Peer> browse_peers(std::chrono::steady_clock::time_point limit);

// --- WebRTC servers (webrtc_servers.go) ------------------------------------------------------------------------------

struct WebRTCTurn {
	std::string addr, user, cred;
};
struct WebRTCSettings {
	std::string stun;
	WebRTCTurn turn;
	std::string dtls_cipher;
};
struct WebRTCServersRequest {
	std::string stun_mode;
	std::vector<std::string> stun;
	std::string dtls_cipher;
	std::string turn_address, turn_username;
	std::optional<std::string> turn_credential;
};
std::vector<std::string> split_stun(const std::string &stun);
std::optional<std::string> ice_address(const std::string &value, std::string &err);
bool valid_hostname(const std::string &host);
bool printable(const std::string &s, std::size_t max);
std::optional<WebRTCSettings> webrtc_servers_config(WebRTCServersRequest req, const WebRTCTurn &current, std::string &err);
std::string webrtc_servers_state_json(const WebRTCSettings &settings);
extern std::function<WebRTCSettings()> read_webrtc;
extern std::function<Error(const WebRTCSettings &)> update_webrtc;
// server.yaml with the WebRTC keys set (config.UpdateWebRTC on a file in yaml.v3's layout).
std::string edit_webrtc_yaml(const std::string &text, const WebRTCSettings &w);
// The edited server.yaml, checked: refused when the file is not a mapping, or not laid out as yaml.v3 writes it.
Error edit_server_yaml(const std::string &text, const WebRTCSettings &w, std::string &edited);

// --- the routes' operations ------------------------------------------------------------------------------------------

// A request as the operations of the routes see it, read on the event loop.
struct Call {
	std::uint64_t id = 0;
	RequestData data;
	std::optional<IP16> local;  // the connection's local address
	std::string host;           // the Host header
	std::string user;           // the signed-in account
};
// The operations answer at once instead of on the blocking pool (set only by the differential check).
extern bool synchronous_calls;

// --- request decoding: the binding and validation of each route, before anything changes -----------------------------
// Each returns nullopt with the answer (code, message) of a request it refuses.

struct Refusal {
	int code;
	std::string msg;
};
template <typename T>
struct Decoded {
	std::optional<T> value;
	Refusal refusal{0, ""};
};

struct EthernetRequest {
	EthernetConfig config;
	bool vlan = false;
	bool force = false;
};
Decoded<EthernetRequest> decode_set_ethernet(const RequestData &data);
Decoded<std::pair<std::string, std::vector<std::string>>> decode_set_dns(const RequestData &data);
struct IPv6Request {
	bool enabled = false;
	std::string interface, mode;
	std::optional<std::string> address, gateway;
};
Decoded<IPv6Request> decode_set_ipv6(const RequestData &data);
Decoded<std::string> decode_set_gateway_preference(const RequestData &data);
Decoded<GatewayCheckConfig> decode_set_gateway_check(const RequestData &data);
Decoded<std::pair<std::string, std::string>> decode_wake(const RequestData &data);  // mac, interface
Decoded<std::pair<std::string, std::string>> decode_set_mac_name(const RequestData &data);
Decoded<std::string> decode_delete_mac(const RequestData &data);
Decoded<std::pair<std::string, std::string>> decode_connect_wifi_no_auth(const RequestData &data);
Decoded<bool> decode_set_wifi_enabled(const RequestData &data);
Decoded<std::string> decode_set_wifi_band_preference(const RequestData &data);
Decoded<WifiProfile> decode_configure_wifi(const RequestData &data);
Decoded<std::string> decode_saved_wifi(const RequestData &data);
Decoded<std::string> decode_set_wifi_country(const RequestData &data);
Decoded<WifiAP> decode_set_wifi_ap(const RequestData &data);
Decoded<std::string> decode_confirm_trial(const RequestData &data);
Decoded<WebRTCServersRequest> decode_set_webrtc_servers(const RequestData &data);

// What the routes do after decoding: the answer's code and message (0, "success" on success).
std::optional<IPv6Policy> ipv6_next_policy(const IPv6Request &req, const IPv6Policy &previous, Refusal &refusal);
std::pair<int, std::string> set_ipv6(const IPv6Request &req);
std::pair<int, std::string> get_ipv6(std::string &json);
std::pair<int, std::string> set_webrtc_servers(const WebRTCServersRequest &req, std::string &state);

} // namespace nkos::network
