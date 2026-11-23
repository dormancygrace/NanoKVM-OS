#pragma once

// Sending, as the Go server's notify sends: HTTP requests with libcurl (20 s, redirects followed, the system's CA
// bundle unless the channel trusts any certificate), e-mail over SMTP with libcurl (implicit TLS, STARTTLS or
// plain; PLAIN or LOGIN, or XOAUTH2 with the channel's sign-in), syslog over UDP, TCP or TLS. Blocking: called
// from the notification threads, never from the event loop.

#include "notify/channels.hpp"
#include "notify/oauth.hpp"

namespace nkos::notify::transport {

HttpAnswer perform(const HttpRequest &request);

// sendChannel: sends an event to a channel; the error has the channel's secrets taken out.
Result send_channel(const Channel &ch, const Rendered &r, Delivery *delivery, OAuth &oauth);

} // namespace nkos::notify::transport
