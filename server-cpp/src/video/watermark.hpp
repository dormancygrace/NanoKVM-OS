#pragma once

// The watermark, as the Go server's common/watermark.go and service/vm/watermark.go: up to 8 PNG blocks the VPSS
// draws into every stream (H.264, H.265, MJPEG, screenshots, previews), placed at a fraction of the free room inside
// a margin and scaled from a 1920-pixel-wide reference. /etc/kvm/watermark.json holds all of it (pictures included).
// GET/POST /api/vm/video/watermark (admin).

#include "http/router.hpp"

namespace nkos::video::watermark {

// Gives the saved watermark to the native library. At start (after kvmv_init, before any stream), synchronously.
void apply_at_start();
// The saved watermark is not the one the native library has (giving it failed): pictures made for people that must
// carry it are withheld. Any thread.
bool unapplied();
// Counts watermark changes (with the capture switches, the preview's picture is stale after one).
std::uint64_t changes();

void add_routes(http::Router &router);

} // namespace nkos::video::watermark
