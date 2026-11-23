#pragma once

// Notifications, as the Go server's service/notify: the standard messages of every language of the web interface
// (embedded from the catalogues at build time) and the words of a journal entry. The channels come later.

#include "http/router.hpp"
#include "system/journal.hpp"

#include <string>
#include <utility>

namespace nkos::system::notify {

// The title and body of a journal entry in a language (English when unknown), its variables filled in.
std::pair<std::string, std::string> describe(const std::string &language, const journal::Entry &entry);

// GET /api/vm/journal (admin): the newest entries in words.
void add_routes(http::Router &router);

} // namespace nkos::system::notify
