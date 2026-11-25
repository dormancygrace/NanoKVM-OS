#pragma once

#include "http/router.hpp"

namespace nkos::auth {

// /api/auth/*: login, logout, account, password, idle sign-out and the users of the Go server's router/auth.go.
void add_routes(http::Router &router);

} // namespace nkos::auth
