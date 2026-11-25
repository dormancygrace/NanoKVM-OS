#pragma once

#include "ryml_all.hpp"

#include <stdexcept>
#include <string>

namespace nkos::yaml {

struct ParseError : std::runtime_error {
	explicit ParseError(ryml::csubstr message) : std::runtime_error(std::string(message.str, message.len)) {}
};

// Per-parser callbacks are also used by the resulting tree, including during alias resolution.
inline ryml::Callbacks parser_callbacks()
{
	ryml::Callbacks callbacks;
	callbacks.set_error_basic([](ryml::csubstr msg, const ryml::ErrorDataBasic &, void *) { throw ParseError(msg); });
	callbacks.set_error_parse([](ryml::csubstr msg, const ryml::ErrorDataParse &, void *) { throw ParseError(msg); });
	callbacks.set_error_visit([](ryml::csubstr msg, const ryml::ErrorDataVisit &, void *) { throw ParseError(msg); });
	return callbacks;
}

} // namespace nkos::yaml
