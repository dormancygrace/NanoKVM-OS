#pragma once

// The web UI files, served as the Go server serves them (server/router/router.go web(), assets.go): index.html never
// cached, content-hashed /assets/ cached for a year with the precompressed .br/.gz chosen by Accept-Encoding, other
// existing files as net/http's FileServer serves them; an absent path falls through to the routes.

#include "http/router.hpp"

#include <string>

namespace nkos::http {

Handler index_no_store();
Handler assets(std::string root);
Handler static_files(std::string root);

// mime.TypeByExtension for the extensions the web UI uses; empty when unknown.
std::string_view mime_type(std::string_view path);

} // namespace nkos::http
