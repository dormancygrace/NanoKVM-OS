#pragma once

// Text on the screen, as the Go server's service/vm/screen_text.go: one picture of the input from VPSS as B, G and R
// planes (up to Full HD), read by the TPU helper /usr/lib/nanokvm-tpu/bin/nanokvm-ocr with the models of one
// language (add-on packages). The web UI's GET /api/vm/screen/text, MCP and PicoClaw read through read(). A switch
// (/etc/kvm/screen-text-off) turns it off. Loop thread only.

#include "http/router.hpp"

#include <array>
#include <atomic>
#include <chrono>
#include <functional>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace nkos::video::screen_text {

// The recognizers a read can ask for.
const std::vector<std::string> &language_names();
// The switch: on unless the off flag is there.
bool enabled();
// The program and the models of at least one language are there.
bool installed();
// Installed and on: the tools that read the screen are offered.
bool available();

struct Area {
	int x = 0, y = 0, w = 0, h = 0;
};

// A piece of a line, in pixels of the whole picture: the upright rectangle around it and its slant.
struct Box {
	std::string text;
	double score = 0;
	int x = 0, y = 0, w = 0, h = 0;
	double slope = 0;
};

struct Request {
	std::string language;                     // "": the first installed
	std::optional<std::array<double, 4>> crop; // x, y, w, h as fractions of the picture
	bool save_picture = false;                 // keep the picture read (/run/nanokvm/screen-text.bgr)
	bool keep_planes = false;                  // hand the picture back (the screen watcher)
	std::uint64_t request_id = 0;              // a web request: the read is cancelled when its connection closes
	std::shared_ptr<const std::atomic<bool>> cancel;  // set: the read is cancelled (the screen watch giving way)
};

struct Reply {
	int code = 0;  // 0 read; else the API's error code with message
	std::string message;
	std::string language;
	int width = 0, height = 0;
	Area area;
	std::vector<Box> boxes;
	std::string stats;  // the helper's summary line (raw JSON), "" when none
	long long capture_ms = 0, total_ms = 0;
	std::vector<unsigned char> planes;  // keep_planes only
};

// Reads the text on the screen; done runs on the loop thread (at once for a read that cannot start).
void read(Request request, std::function<void(Reply)> done);

// The reply's JSON (the "data" of GET /api/vm/screen/text).
std::string reply_json(const Reply &reply);

// The text in reading order (lines top to bottom, pieces of a line left to right).
std::string lines(const std::vector<Box> &boxes);

// The picture size for the current input (screenTextInput): the input with even sides; false with the read's error
// (no signal, or an input over Full HD).
bool input_size(int &width, int &height, std::string &error);

// The TPU takes one model at a time: a read and the screen watch's classifier never run together. Loop thread.
bool tpu_try_lock();
void tpu_unlock();
// Loads the TPU driver unless its device is there (it is kept out at boot; without it the runtime aborts). A blocking
// thread; "" or the error.
std::string load_tpu_driver(std::chrono::milliseconds limit, const std::atomic<bool> *cancel = nullptr);

// GET /api/vm/screen/text, GET/POST /api/vm/screen/text/settings, POST /api/vm/screen/text/language.
void add_routes(http::Router &router);

} // namespace nkos::video::screen_text
