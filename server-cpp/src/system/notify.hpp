#pragma once

// The journal in words (service/vm/journal.go): a journal entry put into the words of the notifications' standard
// messages (notify/model.hpp). The notifications themselves are notify/notify.hpp.

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
