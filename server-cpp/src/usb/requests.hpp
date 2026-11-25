#pragma once

// The USB requests as the Go server binds and validates them (proto.ParseFormRequest: Gin's ShouldBind, then the
// validate tags; c.ShouldBindJSON for the computer sensors). Each returns false where Go answers its "invalid ..."
// error. Form bodies bind the fields by their Go names, as Gin does for fields without a form tag; multipart, XML,
// YAML, TOML and protobuf bodies are refused.

#include "usb/gadget.hpp"

#include <optional>
#include <string>
#include <string_view>

namespace nkos::usb {

struct Body {
	std::string_view content_type, body, query;
};

// proto.SetUSBCompositionReq.
struct CompositionRequest {
	std::optional<bool> keyboard, relative, absolute, network, disk, serial, audio;
	std::string mode, revision;
	std::optional<Identity> identity;
	std::optional<StorageIdentity> storage_identity;
	std::optional<bool> user_disk;
};
bool bind_composition(const Body &in, CompositionRequest &out);

// proto.UpdateVirtualDeviceReq (Device, required).
bool bind_device(const Body &in, std::string &device);
// proto.SetUSBRoleReq (gadget, host or off).
bool bind_role(const Body &in, std::string &role);
// proto.SetUSBInternetReq (both required).
bool bind_flag(const Body &in, bool &enabled, bool &expected);

// c.ShouldBindJSON into struct{Mode string `json:"mode"`}.
bool bind_mode(std::string_view body, std::string &mode);

} // namespace nkos::usb
