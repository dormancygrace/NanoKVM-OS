#pragma once

// server.yaml as the Go server reads it (server/pkg/yamlconf): YAML parsed into a tree whose keys follow Viper's
// rules (case-insensitive, a dotted key is a nested path, null values dropped), and values read with
// mapstructure's weak conversions (quoted numbers, numeric booleans, comma-separated lists).

#include <cstdint>
#include <map>
#include <memory>
#include <optional>
#include <string>
#include <string_view>
#include <vector>

namespace nkos::yaml {

struct Value {
	enum class Kind { scalar, map, seq };
	Kind kind = Kind::scalar;
	std::string text;     // scalar as written (without quotes)
	bool quoted = false;  // a quoted scalar is a string, never a number or boolean
	std::map<std::string, std::shared_ptr<Value>> map;  // lower-case keys
	std::vector<std::shared_ptr<Value>> seq;

	const Value *get(std::string_view key) const;  // one level, key lower-case
	const Value *path(std::string_view dotted) const;
};

// Parses YAML text; nullopt with error set on a syntax error. The top level must be a mapping (or empty).
std::optional<Value> parse(std::string_view text, std::string &error);

// Weak reads, as mapstructure with WeaklyTypedInput. nullopt: absent or not convertible.
std::optional<std::int64_t> as_int(const Value *v);
std::optional<std::uint64_t> as_uint(const Value *v);
std::optional<bool> as_bool(const Value *v);
std::optional<std::string> as_string(const Value *v);
std::optional<std::vector<std::string>> as_strings(const Value *v);

} // namespace nkos::yaml
