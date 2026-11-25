// The NanoKVMs on the local network, as the Go server's service/network/discovery.go: the DNS-SD service file Avahi
// publishes (_nanokvm._tcp), and GET /api/network/peers, which asks each link with one-shot mDNS questions and reads
// the answers. DNS messages are packed and parsed as golang.org/x/net/dns/dnsmessage does, so the same records
// survive the same malformed answers.

#include "network/internal.hpp"
#include "network/network.hpp"

#include "common/log.hpp"
#include "config/config.hpp"
#include "system/hardware.hpp"

#include <algorithm>
#include <arpa/inet.h>
#include <cerrno>
#include <cstring>
#include <netinet/in.h>
#include <poll.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <unistd.h>

namespace nkos::network {

namespace {

constexpr const char *discovery_type = "_nanokvm._tcp";
constexpr const char *discovery_domain = "_nanokvm._tcp.local.";
constexpr std::uint16_t type_a = 1, type_ns = 2, type_cname = 5, type_soa = 6, type_ptr = 12, type_mx = 15, type_txt = 16,
                        type_aaaa = 28, type_srv = 33, type_opt = 41, type_svcb = 64, type_https = 65;

// strings.ToLower: ASCII, and the Latin, Greek and Cyrillic capitals; invalid UTF-8 becomes U+FFFD.
std::string go_to_lower(std::string_view s)
{
	bool ascii = std::all_of(s.begin(), s.end(), [](char c) { return static_cast<unsigned char>(c) < 0x80; });
	std::string out;
	if (ascii) {
		for (char c : s) out += c >= 'A' && c <= 'Z' ? static_cast<char>(c - 'A' + 'a') : c;
		return out;
	}
	auto put = [&](char32_t r) {
		if (r < 0x80) out += static_cast<char>(r);
		else if (r < 0x800) out += static_cast<char>(0xc0 | (r >> 6)), out += static_cast<char>(0x80 | (r & 0x3f));
		else if (r < 0x10000)
			out += static_cast<char>(0xe0 | (r >> 12)), out += static_cast<char>(0x80 | ((r >> 6) & 0x3f)), out += static_cast<char>(0x80 | (r & 0x3f));
		else
			out += static_cast<char>(0xf0 | (r >> 18)), out += static_cast<char>(0x80 | ((r >> 12) & 0x3f)),
				out += static_cast<char>(0x80 | ((r >> 6) & 0x3f)), out += static_cast<char>(0x80 | (r & 0x3f));
	};
	for (std::size_t i = 0; i < s.size();) {
		unsigned char c = static_cast<unsigned char>(s[i]);
		int n = c < 0x80 ? 1 : c >= 0xf0 && c <= 0xf4 ? 4 : c >= 0xe0 ? 3 : c >= 0xc2 && c < 0xe0 ? 2 : 0;
		char32_t r = n == 1 ? c : n == 4 ? c & 7 : n == 3 ? c & 15 : c & 31;
		bool ok = n > 0 && i + n <= s.size();
		for (int k = 1; ok && k < n; k++) {
			unsigned char cc = static_cast<unsigned char>(s[i + k]);
			if ((cc & 0xc0) != 0x80) ok = false;
			r = (r << 6) | (cc & 0x3f);
		}
		if (ok && ((n == 3 && (r < 0x800 || (r >= 0xd800 && r <= 0xdfff))) || (n == 4 && (r < 0x10000 || r > 0x10ffff)))) ok = false;
		if (!ok) {
			put(0xfffd);
			i++;
			continue;
		}
		i += static_cast<std::size_t>(n);
		if (r >= 'A' && r <= 'Z') r += 32;
		else if (r >= 0xc0 && r <= 0xde && r != 0xd7) r += 32;
		else if (r == 0x130) r = 'i';
		else if (r == 0x178) r = 0xff;
		else if ((r >= 0x100 && r <= 0x137) || (r >= 0x14a && r <= 0x177)) r |= 1;
		else if ((r >= 0x139 && r <= 0x148) || (r >= 0x179 && r <= 0x17e)) r += (r & 1);
		else if (r >= 0x391 && r <= 0x3ab && r != 0x3a2) r += 32;
		else if (r >= 0x410 && r <= 0x42f) r += 32;
		else if (r >= 0x400 && r <= 0x40f) r += 80;
		put(r);
	}
	return out;
}

// --- dnsmessage -------------------------------------------------------------------------------------------------------

bool u16(std::string_view msg, std::size_t &off, std::uint16_t &out)
{
	if (off + 2 > msg.size()) return false;
	out = static_cast<std::uint16_t>(static_cast<unsigned char>(msg[off]) << 8 | static_cast<unsigned char>(msg[off + 1]));
	off += 2;
	return true;
}

bool unpack_name(std::string_view msg, std::size_t off, std::string &name, std::size_t &new_off)
{
	std::size_t curr = off;
	new_off = off;
	int ptr = 0;
	name.clear();
	for (;;) {
		if (curr >= msg.size()) return false;
		int c = static_cast<unsigned char>(msg[curr++]);
		switch (c & 0xc0) {
		case 0x00: {
			if (c == 0) goto done;
			std::size_t end = curr + static_cast<std::size_t>(c);
			if (end > msg.size()) return false;
			std::string_view segment = msg.substr(curr, static_cast<std::size_t>(c));
			if (segment.find('.') != std::string_view::npos) return false;
			if (name.size() + segment.size() >= 254) return false;
			name.append(segment);
			name += '.';
			curr = end;
			break;
		}
		case 0xc0: {
			if (curr >= msg.size()) return false;
			int c1 = static_cast<unsigned char>(msg[curr++]);
			if (ptr == 0) new_off = curr;
			if (++ptr > 10) return false;
			curr = static_cast<std::size_t>((c ^ 0xc0) << 8 | c1);
			break;
		}
		default:
			return false;
		}
	}
done:
	if (name.empty()) name = ".";
	if (ptr == 0) new_off = curr;
	return true;
}

bool skip_name(std::string_view msg, std::size_t off, std::size_t &new_off)
{
	new_off = off;
	for (;;) {
		if (new_off >= msg.size()) return false;
		int c = static_cast<unsigned char>(msg[new_off++]);
		switch (c & 0xc0) {
		case 0x00:
			if (c == 0) return true;
			new_off += static_cast<std::size_t>(c);
			if (new_off > msg.size()) return false;
			break;
		case 0xc0:
			new_off++;
			return true;
		default:
			return false;
		}
	}
}

struct Resource {
	std::string name;
	std::uint16_t type = 0;
	std::string ptr;
	SRV srv;
	std::vector<std::string> txt;
	std::array<std::uint8_t, 4> a{};
};

// unpackResourceBody's checks: whether the body of a record of this type parses.
bool unpack_body(std::string_view msg, std::size_t off, std::uint16_t type, std::uint16_t length, Resource &r)
{
	std::string name;
	std::size_t next = 0;
	switch (type) {
	case type_a:
		if (off + 4 > msg.size()) return false;
		for (int i = 0; i < 4; i++) r.a[static_cast<std::size_t>(i)] = static_cast<std::uint8_t>(msg[off + static_cast<std::size_t>(i)]);
		return true;
	case type_ns: case type_cname:
		return unpack_name(msg, off, name, next);
	case type_ptr:
		return unpack_name(msg, off, r.ptr, next);
	case type_soa: {
		if (!unpack_name(msg, off, name, next) || !unpack_name(msg, next, name, next)) return false;
		return next + 20 <= msg.size();
	}
	case type_mx: {
		std::uint16_t pref;
		if (!u16(msg, off, pref)) return false;
		return unpack_name(msg, off, name, next);
	}
	case type_txt: {
		for (std::uint16_t n = 0; n < length;) {
			if (off >= msg.size()) return false;
			std::size_t begin = off + 1, end = begin + static_cast<unsigned char>(msg[off]);
			if (end > msg.size()) return false;
			std::uint16_t len = static_cast<std::uint16_t>(end - begin);
			if (static_cast<std::uint16_t>(length - n) < static_cast<std::uint16_t>(len + 1)) return false;
			n = static_cast<std::uint16_t>(n + len + 1);
			r.txt.emplace_back(msg.substr(begin, len));
			off = end;
		}
		return true;
	}
	case type_aaaa:
		return off + 16 <= msg.size();
	case type_srv: {
		if (!u16(msg, off, r.srv.priority) || !u16(msg, off, r.srv.weight) || !u16(msg, off, r.srv.port)) return false;
		return unpack_name(msg, off, r.srv.target, next);
	}
	case type_svcb: case type_https: {
		std::size_t end = off + length;
		if (end > msg.size()) return false;
		std::uint16_t priority;
		if (!u16(msg, off, priority)) return false;
		if (!unpack_name(msg, off, name, off)) return false;
		int n = 0;
		std::uint16_t previous = 0;
		while (off < end) {
			std::uint16_t key, size;
			if (!u16(msg, off, key)) return false;
			if (n > 0 && key <= previous) return false;
			if (!u16(msg, off, size)) return false;
			if (off + size > end) return false;
			previous = key;
			off += size;
			n++;
		}
		return off == end;
	}
	case type_opt: {
		for (std::size_t old = off; off < old + length;) {
			std::uint16_t code, l;
			if (!u16(msg, off, code) || !u16(msg, off, l)) return false;
			if (msg.size() - off < l) return false;
			off += l;
		}
		return true;
	}
	default:
		return off + length <= msg.size();
	}
}

std::string ip4_string(const std::array<std::uint8_t, 4> &a) { return addr4(a[0], a[1], a[2], a[3]).str(); }

bool contains_string(const std::vector<std::string> &list, const std::string &value)
{
	return std::find(list.begin(), list.end(), value) != list.end();
}

} // namespace

std::string discovery_query(const std::string &name, std::uint16_t type)
{
	if (name.size() > 255) return "";
	std::string msg(12, '\0');
	msg[5] = 1;  // one question
	// Name.pack
	if (name.size() > 254 || name.empty() || name.back() != '.') return "";
	if (name == ".") {
		msg += '\0';
	} else {
		std::size_t begin = 0;
		for (std::size_t i = 0; i < name.size(); i++) {
			if (name[i] != '.') continue;
			if (i - begin >= 64) return "";
			if (i - begin == 0) return "";
			msg += static_cast<char>(i - begin);
			msg.append(name, begin, i - begin);
			begin = i + 1;
		}
		msg += '\0';
	}
	msg += static_cast<char>(type >> 8);
	msg += static_cast<char>(type & 0xff);
	// Class IN with the unicast-response bit.
	msg += static_cast<char>(0x80);
	msg += static_cast<char>(0x01);
	return msg;
}

void DiscoveryAnswers::add(std::string_view msg)
{
	std::size_t off = 0;
	std::uint16_t id, bits, counts[4];
	if (!u16(msg, off, id) || !u16(msg, off, bits)) return;
	for (auto &c : counts)
		if (!u16(msg, off, c)) return;
	if (!(bits & 0x8000)) return;
	for (int q = 0; q < counts[0]; q++) {
		std::size_t next;
		if (!skip_name(msg, off, next) || next + 4 > msg.size()) return;
		off = next + 4;
	}
	// One resource of a section; false at its end or on an error, which ends the section's reading.
	auto resource = [&](Resource &r) {
		std::size_t next;
		if (!unpack_name(msg, off, r.name, next)) return false;
		std::uint16_t cls, ttl_hi, ttl_lo, length;
		if (!u16(msg, next, r.type) || !u16(msg, next, cls) || !u16(msg, next, ttl_hi) || !u16(msg, next, ttl_lo) || !u16(msg, next, length))
			return false;
		if (!unpack_body(msg, next, r.type, length, r)) return false;
		off = next + length;
		return true;
	};
	auto read = [&](int count) {
		for (int i = 0; i < count; i++) {
			Resource r;
			if (!resource(r)) return false;
			std::string name = go_to_lower(r.name);
			switch (r.type) {
			case type_ptr:
				if (name == discovery_domain) instances.insert(r.ptr);
				break;
			case type_srv:
				targets[name] = r.srv;
				break;
			case type_txt:
				texts[name] = r.txt;
				break;
			case type_a: {
				std::string address = ip4_string(r.a);
				if (!contains_string(addresses[name], address)) addresses[name].push_back(address);
				break;
			}
			}
		}
		return true;
	};
	if (!read(counts[1])) return;
	// SkipAllAuthorities.
	for (int i = 0; i < counts[2]; i++) {
		std::size_t next;
		std::uint16_t skip;
		if (!skip_name(msg, off, next) || next + 8 > msg.size()) return;
		next += 8;
		if (!u16(msg, next, skip)) return;
		if (next + skip > msg.size()) return;
		off = next + skip;
	}
	read(counts[3]);
}

std::vector<std::string> DiscoveryAnswers::missing() const
{
	std::vector<std::string> questions;
	for (const auto &instance : instances) {
		std::string key = go_to_lower(instance);
		if (!targets.count(key)) questions.push_back(discovery_query(instance, type_srv));
		if (!texts.count(key)) questions.push_back(discovery_query(instance, type_txt));
	}
	for (const auto &[_, target] : targets) {
		auto it = addresses.find(go_to_lower(target.target));
		if (it == addresses.end() || it->second.empty()) questions.push_back(discovery_query(target.target, type_a));
	}
	return questions;
}

std::vector<Peer> DiscoveryAnswers::peers() const
{
	std::vector<Peer> out;
	for (const auto &instance : instances) {
		std::string key = go_to_lower(instance);
		auto target = targets.find(key);
		if (target == targets.end()) continue;
		const std::string &host = target->second.target;
		Peer peer;
		peer.name = discovery_instance_name(instance);
		peer.host = host.size() && host.back() == '.' ? host.substr(0, host.size() - 1) : host;
		if (auto it = addresses.find(go_to_lower(host)); it != addresses.end()) peer.addresses = it->second;
		peer.port = target->second.port;
		peer.proto = "https";
		if (auto it = texts.find(key); it != texts.end()) {
			for (const auto &text : it->second) {
				if (text.rfind("proto=", 0) == 0) {
					std::string value = text.substr(6);
					if (value == "http" || value == "https") peer.proto = value;
				}
				if (text.rfind("model=", 0) == 0 && text.size() - 6 <= 32) peer.model = text.substr(6);
			}
		}
		out.push_back(peer);
	}
	return out;
}

std::vector<Peer> collect_peers(const std::vector<LinkPeers> &links)
{
	std::vector<Peer> peers;
	auto same = [](const Peer &a, const Peer &b) {
		return a.name == b.name && a.host == b.host && a.port == b.port && a.proto == b.proto && a.model == b.model &&
		       a.self == b.self && a.addresses == b.addresses;
	};
	for (const auto &link : links) {
		for (auto peer : link.peers) {
			for (const auto &address : peer.addresses) {
				if (contains_string(link.local, address)) {
					peer.self = true;
					break;
				}
			}
			if (std::none_of(peers.begin(), peers.end(), [&](const Peer &p) { return same(p, peer); })) peers.push_back(peer);
		}
	}
	return peers;
}

std::string discovery_instance_name(const std::string &instance)
{
	std::string suffix = std::string(".") + discovery_domain;
	std::string label = instance;
	if (label.size() >= suffix.size() && label.compare(label.size() - suffix.size(), suffix.size(), suffix) == 0)
		label.resize(label.size() - suffix.size());
	std::string out;
	auto digits = [&](std::size_t at) {
		for (std::size_t k = at; k < at + 3; k++)
			if (label[k] < '0' || label[k] > '9') return false;
		return true;
	};
	for (std::size_t i = 0; i < label.size(); i++) {
		if (label[i] != '\\' || i + 1 >= label.size()) {
			out += label[i];
			continue;
		}
		if (i + 3 < label.size() && digits(i + 1)) {
			out += static_cast<char>(((label[i + 1] - '0') * 10 + (label[i + 2] - '0')) * 10 + (label[i + 3] - '0'));
			i += 3;
			continue;
		}
		out += label[i + 1];
		i++;
	}
	return out;
}

std::string peers_json(const std::vector<Peer> &peers)
{
	std::string out = "[";
	for (std::size_t i = 0; i < peers.size(); i++) {
		const auto &p = peers[i];
		out += (i ? "," : "") + std::string("{\"name\":") + quote(p.name) + ",\"host\":" + quote(p.host) +
		       ",\"addresses\":" + json_strings(p.addresses, false) + ",\"port\":" + std::to_string(p.port) +
		       ",\"proto\":" + quote(p.proto) + ",\"model\":" + quote(p.model) + ",\"self\":" + (p.self ? "true" : "false") + "}";
	}
	return out + "]";
}

std::string discovery_service(const std::string &proto, int http_port, int https_port, const std::string &model)
{
	std::string scheme = "https";
	int port = https_port;
	if (proto != "https") scheme = "http", port = http_port;
	return std::string("<?xml version=\"1.0\" standalone='no'?>\n"
	                   "<!DOCTYPE service-group SYSTEM \"avahi-service.dtd\">\n"
	                   "<!-- Written by NanoKVM-Server at start; changes are replaced. -->\n"
	                   "<service-group>\n"
	                   "  <name replace-wildcards=\"yes\">%h</name>\n"
	                   "  <service>\n"
	                   "    <type>") +
	       discovery_type + "</type>\n    <port>" + std::to_string(port) + "</port>\n    <txt-record>proto=" + scheme +
	       "</txt-record>\n    <txt-record>model=" + model + "</txt-record>\n  </service>\n</service-group>\n";
}

void publish_discovery_file(const std::string &want)
{
	const std::string &file = paths.discovery_file;
	if (auto have = read_file(file); have && *have == want) {
		// Avahi reads it as its own user, whatever the server's umask was.
		struct stat st{};
		if (::stat(file.c_str(), &st) == 0 && (st.st_mode & 0777) == 0644) return;
	}
	std::string tmp = file + ".new";
	Error err = write_file(tmp, want, 0644);
	if (!err) err = chmod_path(tmp, 0644);
	if (!err) err = rename(tmp, file);
	if (err) {
		(void)remove(tmp);
		log::warn("discovery: the service is not announced: {}", err.text);
	}
}

void publish_discovery()
{
	const auto &conf = config::get();
	publish_discovery_file(discovery_service(conf.proto, conf.port.http, conf.port.https, system::hardware().version));
}

// browsePeers: every link the device is on, then each link once or twice more for what its answers left out.
std::vector<Peer> browse_peers(std::chrono::steady_clock::time_point limit)
{
	struct Link {
		int fd;
		int index;
		DiscoveryAnswers answers;
		std::vector<std::string> local;
	};
	std::vector<Link> links;
	bool ok = true;
	auto list = interfaces(&ok);
	if (!ok) return {};
	for (const auto &iface : list) {
		// IFF_UP, IFF_MULTICAST, IFF_LOOPBACK
		if (!(iface.flags & 1) || !(iface.flags & 0x1000) || (iface.flags & 8)) continue;
		// The USB network leads to the managed computer only, which is no NanoKVM and must not be able to show itself
		// as one.
		if (iface.name == "usb0") continue;
		std::vector<std::string> local;
		for (const auto &a : iface.addrs)
			if (ip_is_v4(a.ip)) local.push_back(ip_string(a.ip));
		int fd = ::socket(AF_INET, SOCK_DGRAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
		if (fd < 0) continue;
		sockaddr_in any{};
		any.sin_family = AF_INET;
		ip_mreqn mreq{};
		mreq.imr_ifindex = iface.index;
		int on = 1;
		if (::bind(fd, reinterpret_cast<sockaddr *>(&any), sizeof any) != 0 ||
		    ::setsockopt(fd, IPPROTO_IP, IP_MULTICAST_IF, &mreq, sizeof mreq) != 0 ||
		    ::setsockopt(fd, IPPROTO_IP, IP_PKTINFO, &on, sizeof on) != 0) {
			::close(fd);
			continue;
		}
		links.push_back(Link{fd, iface.index, {}, local});
	}
	sockaddr_in group{};
	group.sin_family = AF_INET;
	group.sin_port = htons(5353);
	group.sin_addr.s_addr = htonl(0xe00000fb);  // 224.0.0.251
	auto ask = [&](Link &link, const std::vector<std::string> &questions) {
		for (const auto &q : questions)
			if (!q.empty()) (void)::sendto(link.fd, q.data(), q.size(), 0, reinterpret_cast<sockaddr *>(&group), sizeof group);
	};
	auto listen = [&](std::chrono::milliseconds wait) {
		auto deadline = std::min(std::chrono::steady_clock::now() + wait, limit);
		std::vector<char> buffer(9000);
		for (;;) {
			auto left = std::chrono::duration_cast<std::chrono::milliseconds>(deadline - std::chrono::steady_clock::now()).count();
			if (left <= 0) return;
			std::vector<pollfd> fds;
			for (const auto &link : links) fds.push_back({link.fd, POLLIN, 0});
			if (fds.empty()) return;
			int n = ::poll(fds.data(), fds.size(), static_cast<int>(left));
			if (n < 0 && errno != EINTR) return;
			for (std::size_t i = 0; i < fds.size(); i++) {
				if (!(fds[i].revents & POLLIN)) continue;
				for (;;) {
					alignas(cmsghdr) char control[256];
					iovec iov{buffer.data(), buffer.size()};
					msghdr msg{};
					msg.msg_iov = &iov;
					msg.msg_iovlen = 1;
					msg.msg_control = control;
					msg.msg_controllen = sizeof control;
					ssize_t got = ::recvmsg(links[i].fd, &msg, 0);
					if (got < 0) break;
					int index = -1;
					for (cmsghdr *c = CMSG_FIRSTHDR(&msg); c; c = CMSG_NXTHDR(&msg, c))
						if (c->cmsg_level == IPPROTO_IP && c->cmsg_type == IP_PKTINFO)
							index = reinterpret_cast<in_pktinfo *>(CMSG_DATA(c))->ipi_ifindex;
					if (index != links[i].index) continue;
					links[i].answers.add(std::string_view(buffer.data(), static_cast<std::size_t>(got)));
				}
			}
		}
	};
	for (auto &link : links) ask(link, {discovery_query(discovery_domain, type_ptr)});
	listen(std::chrono::milliseconds(1200));
	// Twice at most: the second round asks for the addresses of services that were only told in the first.
	for (auto wait : {std::chrono::milliseconds(800), std::chrono::milliseconds(600)}) {
		bool asked = false;
		for (auto &link : links) {
			if (auto questions = link.answers.missing(); !questions.empty()) {
				ask(link, questions);
				asked = true;
			}
		}
		if (!asked || std::chrono::steady_clock::now() >= limit) break;
		listen(wait);
	}
	std::vector<LinkPeers> results;
	for (auto &link : links) {
		results.push_back(LinkPeers{link.answers.peers(), link.local});
		::close(link.fd);
	}
	return collect_peers(results);
}

} // namespace nkos::network
