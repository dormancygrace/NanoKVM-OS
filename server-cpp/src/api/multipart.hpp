#pragma once

// multipart/form-data bodies (uploads), as Gin's FormFile and PostForm read them: the parts by field name; a file
// part's name is the base of the browser's filename (Go's multipart.Part.FileName).

#include "http/router.hpp"

#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::api {

struct Part {
	std::string name;
	std::optional<std::string> filename;  // a file part
	std::string_view data;                // into the request body
};

// The parts of a multipart/form-data request, or nullopt when it is none or does not parse.
std::optional<std::vector<Part>> multipart(const http::Request &request);
// The same for a Content-Type and a body (the parts point into body).
std::optional<std::vector<Part>> multipart(std::string_view content_type, std::string_view body);

// The first file part of field, or nullptr.
const Part *form_file(const std::vector<Part> &parts, std::string_view field);
// The first plain part of field.
std::optional<std::string> form_value(const std::vector<Part> &parts, std::string_view field);

} // namespace nkos::api
