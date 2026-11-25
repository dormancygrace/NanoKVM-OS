#include "video/rtc_common.hpp"

#include "common/gojson.hpp"
#include "network/network.hpp"

#include <libwebsockets.h>
#include <openssl/rand.h>

#include <algorithm>
#include <arpa/inet.h>
#include <cctype>
#include <cstdlib>
#include <netinet/in.h>
#include <sys/socket.h>

namespace nkos::video::rtc_common {

std::pair<std::vector<rtc::IceServer>, std::string> ice_servers()
{
	// The settings as they are now: POST /api/network/webrtc changes them while the server runs.
	const auto c = network::webrtc_servers();
	std::vector<rtc::IceServer> servers;
	std::vector<std::string> urls;
	std::string field;
	auto flush = [&] {
		if (!field.empty() && field != "disable") urls.push_back("stun:" + field);
		field.clear();
	};
	for (char ch : c.stun) {
		if (ch == ',' || ch == ' ' || ch == '\t') flush();
		else field += ch;
	}
	flush();
	std::string json = "[";
	if (!urls.empty()) {
		json += "{\"urls\":[";
		for (std::size_t i = 0; i < urls.size(); i++) {
			json += (i ? "," : "") + gojson::quote(urls[i]);
			try {
				servers.emplace_back(urls[i]);
			} catch (...) {
			}
		}
		json += "]}";
	}
	if (!c.turn_addr.empty() && !c.turn_user.empty() && !c.turn_cred.empty()) {
		std::string url = "turn:" + c.turn_addr;
		json += std::string(urls.empty() ? "" : ",") + "{\"urls\":[" + gojson::quote(url) + "],\"username\":" +
		        gojson::quote(c.turn_user) + ",\"credential\":" + gojson::quote(c.turn_cred) + "}";
		auto colon = c.turn_addr.rfind(':');
		std::string host = colon == std::string::npos ? c.turn_addr : c.turn_addr.substr(0, colon);
		auto port = static_cast<std::uint16_t>(colon == std::string::npos ? 3478 : std::atoi(c.turn_addr.c_str() + colon + 1));
		servers.emplace_back(host, port, c.turn_user, c.turn_cred, rtc::IceServer::RelayType::TurnUdp);
	}
	return {servers, json + "]"};
}

std::string local_address(const http::Request &req)
{
	if (!req.wsi) return "";
	lws *net = lws_get_network_wsi(req.wsi);
	int fd = lws_get_socket_fd(net ? net : req.wsi);
	sockaddr_storage addr{};
	socklen_t len = sizeof addr;
	if (fd < 0 || ::getsockname(fd, reinterpret_cast<sockaddr *>(&addr), &len) != 0) return "";
	char text[INET6_ADDRSTRLEN] = {};
	if (addr.ss_family == AF_INET) {
		auto *a = reinterpret_cast<sockaddr_in *>(&addr);
		if ((ntohl(a->sin_addr.s_addr) >> 24) == 127) return "";
		inet_ntop(AF_INET, &a->sin_addr, text, sizeof text);
	} else if (addr.ss_family == AF_INET6) {
		auto *a = reinterpret_cast<sockaddr_in6 *>(&addr);
		if (IN6_IS_ADDR_LOOPBACK(&a->sin6_addr)) return "";
		if (IN6_IS_ADDR_V4MAPPED(&a->sin6_addr)) {
			if (a->sin6_addr.s6_addr[12] == 127) return "";
			inet_ntop(AF_INET, &a->sin6_addr.s6_addr[12], text, sizeof text);
		} else {
			inet_ntop(AF_INET6, &a->sin6_addr, text, sizeof text);
		}
	}
	return text;
}

rtc::Configuration configuration(const std::vector<rtc::IceServer> &servers, const std::string &bind_address,
                                 std::size_t mtu)
{
	// The suites the Go server offered, the saved preference first. libdatachannel reads the list when the
	// connection's DTLS transport starts.
	static const std::string chacha = "ECDHE-ECDSA-CHACHA20-POLY1305:ECDHE-RSA-CHACHA20-POLY1305";
	static const std::string aes = "ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256";
	rtc::SetDtlsCipherList(network::webrtc_servers().dtls_cipher == "aes-gcm" ? aes + ":" + chacha : chacha + ":" + aes);
	rtc::Configuration config;
	config.iceServers = servers;
	config.preferDtlsServer = true;  // The device chooses the negotiated suite for normal browser offers.
	if (!bind_address.empty()) config.bindAddress = bind_address;
	if (mtu) config.mtu = mtu;
	config.certificateType = rtc::CertificateType::Ecdsa;
	config.disableAutoNegotiation = true;
	return config;
}

void trim_to(rtc::Description::Media &media, int pt, const std::optional<std::string> &fmtp, std::uint32_t ssrc,
             const std::string &cname, const std::string &stream)
{
	for (int other : media.payloadTypes())
		if (other != pt) media.removeFormat(std::to_string(other));
	if (fmtp) {
		if (auto *map = media.rtpMap(pt)) {
			map->fmtps.clear();
			map->fmtps.push_back(*fmtp);
		}
	}
	media.setDirection(rtc::Description::Direction::SendOnly);
	media.addSSRC(ssrc, cname, stream, cname);
}

std::optional<int> find_payload(const rtc::Description::Media &media, const std::string &format,
                                const std::function<int(const std::string &fmtp)> &rank)
{
	std::optional<int> best;
	int best_rank = 0;
	for (int pt : media.payloadTypes()) {
		auto *map = const_cast<rtc::Description::Media &>(media).rtpMap(pt);
		if (!map) continue;
		std::string f = map->format, want = format;
		std::transform(f.begin(), f.end(), f.begin(), [](unsigned char c) { return std::tolower(c); });
		std::transform(want.begin(), want.end(), want.begin(), [](unsigned char c) { return std::tolower(c); });
		if (f != want) continue;
		std::string fmtp;
		for (auto &x : map->fmtps) fmtp += x + ";";
		int r = rank ? rank(fmtp) : 1;
		if (r > best_rank) {
			best_rank = r;
			best = pt;
		}
	}
	return best;
}

std::string description_json(const std::string &type, const std::string &sdp)
{
	return "{\"type\":" + gojson::quote(type) + ",\"sdp\":" + gojson::quote(sdp) + "}";
}

std::string candidate_json(const rtc::Candidate &candidate)
{
	return "{\"candidate\":" + gojson::quote(std::string(candidate)) + ",\"sdpMid\":" + gojson::quote(candidate.mid()) +
	       ",\"sdpMLineIndex\":0,\"usernameFragment\":null}";
}

std::uint32_t random_u32()
{
	std::uint32_t v = 0;
	RAND_bytes(reinterpret_cast<unsigned char *>(&v), sizeof v);
	return v;
}

} // namespace nkos::video::rtc_common
