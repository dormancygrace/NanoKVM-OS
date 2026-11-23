#pragma once

// Date and time (System > Date and time), as the Go server's service/vm/date_time.go and timeconfig: NTP servers
// (chrony), time zone, 12/24-hour and date formats, the clock set from the browser where no NTP reaches; and the
// web interface title (/etc/kvm/web-title).

#include "http/router.hpp"

namespace nkos::system {

// GET/POST /api/vm/date-time, POST /api/vm/date-time/clock, GET/POST /api/vm/web-title.
void add_time_routes(http::Router &router);

} // namespace nkos::system
