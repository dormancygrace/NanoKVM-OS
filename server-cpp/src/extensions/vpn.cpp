#include "extensions/vpn.hpp"

#include "network/util.hpp"

#include <map>

namespace nkos::extensions::vpn {

namespace {

bool digit(char c) { return c >= '0' && c <= '9'; }
bool suffix_char(char c)
{
	return digit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '.' || c == '-';
}

// The match at start, or 0.
std::size_t match_at(std::string_view s, std::size_t start, bool three_parts)
{
	std::size_t i = start;
	auto digits = [&] {
		std::size_t from = i;
		while (i < s.size() && digit(s[i])) i++;
		return i > from;
	};
	if (!digits() || i >= s.size() || s[i] != '.') return 0;
	i++;
	if (!digits()) return 0;
	if (i + 1 < s.size() && s[i] == '.' && digit(s[i + 1])) {
		i++;
		digits();
	} else if (three_parts) {
		return 0;
	}
	while (i + 1 < s.size() && (s[i] == '-' || s[i] == '+') && suffix_char(s[i + 1])) {
		i++;
		while (i < s.size() && suffix_char(s[i])) i++;
	}
	return i - start;
}

// The VPN pages ask every 5 s; a version only changes with the program file.
ToolCache &cache()
{
	static ToolCache c(
		[](const std::string &path, const std::vector<std::string> &args) {
			Command cmd;
			cmd.argv = {path};
			cmd.argv.insert(cmd.argv.end(), args.begin(), args.end());
			cmd.timeout = std::chrono::seconds(2);
			auto r = run(cmd);
			return std::pair{r.out, r.error};
		},
		std::chrono::minutes(1));
	return c;
}

} // namespace

std::string find_version(std::string_view text, bool three_parts)
{
	for (std::size_t start = 0; start < text.size(); start++)
		if (std::size_t n = match_at(text, start, three_parts)) return std::string(text.substr(start, n));
	return "";
}

Reply versions()
{
	static const std::map<std::string, std::string> tools{
		{"netbird", "netbird"}, {"openvpn", "openvpn"}, {"tailscale", "tailscale"}, {"wireguard", "wg"},
		{"zerotier", "zerotier-one"}};
	// versionFlag: --version except for the tools that have another switch.
	static const std::map<std::string, std::string> version_flag{{"zerotier", "-v"}};
	std::string data = "{";
	bool first = true;
	for (const auto &[name, tool] : tools) {
		auto path = look_path(tool);
		if (!path) continue;
		auto flag = version_flag.find(name);
		auto [out, err] = cache().output(*path, {flag == version_flag.end() ? "--version" : flag->second});
		if (!err.empty()) continue;
		std::string line = out.substr(0, out.find('\n'));
		data += (first ? "" : ",") + q(name) + ":" + q(find_version(line, false));
		first = false;
	}
	return ok(data + "}");
}

bool purge(const std::string &body)
{
	network::RequestData data;
	data.method = "POST";
	data.body = body;
	auto b = network::Binder::bind(data, true);
	bool value = false;
	b.boolean("purge", "Purge", value);
	return b.ok() && value;
}

std::string remove_state(const std::vector<std::string> &paths)
{
	std::string joined;
	for (const auto &path : paths)
		if (auto e = remove_all(path); !e.empty()) joined += (joined.empty() ? "" : "\n") + e;
	return joined;
}

} // namespace nkos::extensions::vpn
