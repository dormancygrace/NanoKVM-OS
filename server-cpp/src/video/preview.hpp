#pragma once

// GET /api/vm/screen/preview, as the Go server's service/vm/preview.go: the overview page's small still picture of
// the host screen (at most 640x360, JPEG quality 60, base64), taken at most once per 5 s however many browsers ask
// (1 s while there is no picture), through a capture lease: it starts no stream and counts as no viewer, and does
// not switch on a capture that was switched off. It comes from a VPSS channel of its own and carries the watermark;
// while the saved watermark is not applied there is no picture.

#include "http/router.hpp"

namespace nkos::video::preview {

void add_routes(http::Router &router);

} // namespace nkos::video::preview
