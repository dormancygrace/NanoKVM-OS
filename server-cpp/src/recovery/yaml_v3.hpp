#pragma once

// The two uses settings recovery makes of server.yaml, byte for byte as the Go server's gopkg.in/yaml.v3 does them:
// the port check of an archive (yaml.Unmarshal into a struct with port.http and port.https as int), and the reset
// of the web settings in a restored or kept file (yaml.Unmarshal into map[string]any, delete "jwt", set
// authentication to enable, yaml.Marshal). The file is parsed with rapidyaml; values are resolved with yaml.v3's
// rules (YAML 1.2 core schema, 0777 octal, timestamps), and written by a port of yaml.v3's emitter for what such a
// map can hold (block mappings and sequences with four-space indentation, keys in yaml.v3's order, scalars in the
// style yaml.v3 chooses).

#include <functional>
#include <optional>
#include <string>
#include <string_view>

namespace nkos::recovery::yamlv3 {

// Validate's check: true when both ports parse as ints in 1..65535 and the document decodes without error.
bool valid_ports(std::string_view data);

// The new file, or nullopt with error set (yaml.v3's message where it can be matched). panicked is set for a file
// on which the Go server panics (an empty or null document: assignment to a nil map).
std::optional<std::string> reset_web_settings(std::string_view data, std::string &error, bool &panicked);

// The YAML half of the developer archive's settings redaction (redactSettings): the document decoded into an
// interface{} as yaml.v3 does (nullopt for a syntax error, no document, duplicate keys, or any anchor or alias), the
// value of every mapping key for which secret(key) holds replaced by "<hidden>" with all below it, and the result
// written by yaml.Marshal.
std::optional<std::string> redact(std::string_view data, const std::function<bool(std::string_view key)> &secret);

} // namespace nkos::recovery::yamlv3
