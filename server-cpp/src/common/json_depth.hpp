#pragma once

namespace nkos {

// Request parsers recurse on 128 KiB musl worker stacks. The root value counts as one level.
inline constexpr int max_json_depth = 32;

} // namespace nkos
