#include "usb/requests.hpp"

#include "usb/gojson_read.hpp"
#include "usb/gotext.hpp"

namespace nkos::usb {

namespace {

enum class Binding { json, form, refused };

// binding.Default for PUT/POST with Gin's ContentType() (the media type exactly as sent, parameters cut off).
Binding binding_for(std::string_view content_type)
{
	std::string type = goj::media_type(content_type);
	if (type == "application/json") return Binding::json;
	if (type == "application/xml" || type == "text/xml" || type == "application/x-protobuf" ||
	    type == "application/x-yaml" || type == "application/yaml" || type == "application/toml" ||
	    type == "multipart/form-data")
		return Binding::refused;
	return Binding::form;
}

// http.Request.ParseForm for PUT/POST: the url-encoded body (when its media type, case-insensitive, says so) first,
// then the query.
bool form_values(const Body &in, goj::FormValues &out)
{
	std::string type = goj::media_type(in.content_type);
	for (auto &c : type) c = static_cast<char>(std::tolower(static_cast<unsigned char>(c)));
	bool ok = true;
	if (type == "application/x-www-form-urlencoded") ok = goj::parse_form(in.body, out);
	goj::FormValues query;
	ok = goj::parse_form(in.query, query) && ok;
	out.values.insert(out.values.end(), query.values.begin(), query.values.end());
	return ok;
}

// Gin's setBoolField (after its TrimSpace): "" is false; strconv.ParseBool.
bool form_bool(const goj::FormValues &form, const char *key, std::optional<bool> &out)
{
	auto v = form.get(key);
	if (!v) return true;
	std::string s = gotext::trim_space(*v);
	if (s.empty() || s == "0" || s == "f" || s == "F" || s == "false" || s == "FALSE" || s == "False") out = false;
	else if (s == "1" || s == "t" || s == "T" || s == "true" || s == "TRUE" || s == "True") out = true;
	else return false;
	return true;
}

void form_string(const goj::FormValues &form, const char *key, std::string &out)
{
	if (auto v = form.get(key)) out = *v;
}

bool decode_json(std::string_view body, const std::function<void(goj::Reader &)> &read)
{
	std::size_t end;
	std::string error;
	return goj::decode(body, "", read, end, error);
}

} // namespace

bool bind_composition(const Body &in, CompositionRequest &r)
{
	r = {};
	auto binding = binding_for(in.content_type);
	if (binding == Binding::refused) return false;
	if (binding == Binding::json) {
		bool ok = decode_json(in.body, [&](goj::Reader &rd) {
			rd.object("SetUSBCompositionReq", "proto.SetUSBCompositionReq", {
				{"keyboard", [&](goj::Reader &x) { x.pointer_boolean(r.keyboard); }},
				{"relative", [&](goj::Reader &x) { x.pointer_boolean(r.relative); }},
				{"absolute", [&](goj::Reader &x) { x.pointer_boolean(r.absolute); }},
				{"network", [&](goj::Reader &x) { x.pointer_boolean(r.network); }},
				{"disk", [&](goj::Reader &x) { x.pointer_boolean(r.disk); }},
				{"serial", [&](goj::Reader &x) { x.pointer_boolean(r.serial); }},
				{"audio", [&](goj::Reader &x) { x.pointer_boolean(r.audio); }},
				{"mode", [&](goj::Reader &x) { x.string(r.mode); }},
				{"revision", [&](goj::Reader &x) { x.string(r.revision); }},
				{"identity", [&](goj::Reader &x) {
					x.pointer_object("USBIdentityReq", "proto.USBIdentityReq", [&] {
						if (!r.identity) r.identity = Identity{};
						auto *id = &*r.identity;
						return std::vector<goj::Field>{
							{"vendorId", [id](goj::Reader &y) { y.string(id->vendor_id); }},
							{"productId", [id](goj::Reader &y) { y.string(id->product_id); }},
							{"manufacturer", [id](goj::Reader &y) { y.string(id->manufacturer); }},
							{"product", [id](goj::Reader &y) { y.string(id->product); }},
							{"serialNumber", [id](goj::Reader &y) { y.string(id->serial_number); }},
						};
					}, [&] { r.identity.reset(); });
				}},
				{"storageIdentity", [&](goj::Reader &x) {
					x.pointer_object("Identity", "storageidentity.Identity", [&] {
						if (!r.storage_identity) r.storage_identity = StorageIdentity{};
						auto *id = &*r.storage_identity;
						return std::vector<goj::Field>{
							{"vendor", [id](goj::Reader &y) { y.string(id->vendor); }},
							{"image", [id](goj::Reader &y) { y.string(id->image); }},
							{"setup", [id](goj::Reader &y) { y.string(id->setup); }},
							{"user", [id](goj::Reader &y) { y.string(id->user); }},
						};
					}, [&] { r.storage_identity.reset(); });
				}},
				{"userDisk", [&](goj::Reader &x) { x.pointer_boolean(r.user_disk); }},
			});
		});
		if (!ok) return false;
	} else {
		goj::FormValues form;
		if (!form_values(in, form)) return false;
		bool ok = form_bool(form, "Keyboard", r.keyboard) && form_bool(form, "Relative", r.relative) &&
		          form_bool(form, "Absolute", r.absolute) && form_bool(form, "Network", r.network) &&
		          form_bool(form, "Disk", r.disk) && form_bool(form, "Serial", r.serial) &&
		          form_bool(form, "Audio", r.audio) && form_bool(form, "UserDisk", r.user_disk);
		if (!ok) return false;
		form_string(form, "Mode", r.mode);
		form_string(form, "Revision", r.revision);
	}
	// validate: required pointers, required,oneof mode, required revision.
	return r.keyboard && r.relative && r.absolute && r.network && r.disk && r.serial && r.audio &&
	       (r.mode == "normal" || r.mode == "hid-only") && !r.revision.empty();
}

bool bind_device(const Body &in, std::string &device)
{
	device.clear();
	auto binding = binding_for(in.content_type);
	if (binding == Binding::refused) return false;
	if (binding == Binding::json) {
		if (!decode_json(in.body, [&](goj::Reader &rd) {
			    rd.object("UpdateVirtualDeviceReq", "proto.UpdateVirtualDeviceReq",
			              {{"Device", [&](goj::Reader &x) { x.string(device); }}});
		    }))
			return false;
	} else {
		goj::FormValues form;
		if (!form_values(in, form)) return false;
		form_string(form, "Device", device);
	}
	return !device.empty();
}

bool bind_role(const Body &in, std::string &role)
{
	role.clear();
	auto binding = binding_for(in.content_type);
	if (binding == Binding::refused) return false;
	if (binding == Binding::json) {
		if (!decode_json(in.body, [&](goj::Reader &rd) {
			    rd.object("SetUSBRoleReq", "proto.SetUSBRoleReq", {{"role", [&](goj::Reader &x) { x.string(role); }}});
		    }))
			return false;
	} else {
		goj::FormValues form;
		if (!form_values(in, form)) return false;
		form_string(form, "Role", role);
	}
	return role == "gadget" || role == "host" || role == "off";
}

bool bind_flag(const Body &in, bool &enabled, bool &expected)
{
	std::optional<bool> e, x;
	auto binding = binding_for(in.content_type);
	if (binding == Binding::refused) return false;
	if (binding == Binding::json) {
		if (!decode_json(in.body, [&](goj::Reader &rd) {
			    rd.object("SetUSBInternetReq", "proto.SetUSBInternetReq", {
				    {"enabled", [&](goj::Reader &y) { y.pointer_boolean(e); }},
				    {"expectedEnabled", [&](goj::Reader &y) { y.pointer_boolean(x); }},
			    });
		    }))
			return false;
	} else {
		goj::FormValues form;
		if (!form_values(in, form)) return false;
		if (!form_bool(form, "Enabled", e) || !form_bool(form, "ExpectedEnabled", x)) return false;
	}
	if (!e || !x) return false;
	enabled = *e;
	expected = *x;
	return true;
}

bool bind_mode(std::string_view body, std::string &mode)
{
	mode.clear();
	return decode_json(body, [&](goj::Reader &rd) {
		rd.object("", "struct { Mode string \"json:\\\"mode\\\"\" }", {{"mode", [&](goj::Reader &x) { x.string(mode); }}});
	});
}

} // namespace nkos::usb
