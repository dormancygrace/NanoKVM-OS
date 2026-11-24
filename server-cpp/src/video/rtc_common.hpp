#pragma once

// What every WebRTC connection of the server shares (video, audio): the ICE servers and DTLS cipher order of the
// WebRTC settings as they are when the connection is made (the Go server's iceservers and webrtcdtls), the device address the page reached (icelocal: the media stays on it), the peer connection
// configuration, and trimming a reciprocated m-line to one codec.

#include "http/router.hpp"

#include <rtc/rtc.hpp>

#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace nkos::video::rtc_common {

// The servers in libdatachannel's form, and as the page gets them (JSON array of {urls, username, credential}).
std::pair<std::vector<rtc::IceServer>, std::string> ice_servers();

// The local address of the request's connection; empty for a loopback one (a local proxy relays a browser
// elsewhere: every address then).
std::string local_address(const http::Request &req);

// A peer connection configuration for the page at bind_address (empty: any), with the shared ECDSA certificate
// and the saved DTLS cipher preference.
rtc::Configuration configuration(const std::vector<rtc::IceServer> &servers, const std::string &bind_address,
                                 std::size_t mtu);

// Keeps only payload type pt of media (its rtpmap; fmtp replaced when given), sendonly, with ssrc.
void trim_to(rtc::Description::Media &media, int pt, const std::optional<std::string> &fmtp, std::uint32_t ssrc,
             const std::string &cname, const std::string &stream);

// The page's payload type for a codec name ("opus", "H264" with predicate on the fmtp, ...).
std::optional<int> find_payload(const rtc::Description::Media &media, const std::string &format,
                                const std::function<int(const std::string &fmtp)> &rank);

// The answer and candidates as the page reads them.
std::string description_json(const std::string &type, const std::string &sdp);
std::string candidate_json(const rtc::Candidate &candidate);

std::uint32_t random_u32();

} // namespace nkos::video::rtc_common
