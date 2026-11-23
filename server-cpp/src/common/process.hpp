#pragma once

// External commands, as the Go server runs them with exec.CommandContext: argv (no shell), optional input on stdin,
// combined stdout and stderr, killed when the timeout passes. Blocking: call it off the event loop
// (loop::blocking).

#include <atomic>
#include <chrono>
#include <string>
#include <vector>

namespace nkos::process {

struct Result {
	bool started = false;   // false: the program could not be run (error says why)
	int exit_code = -1;     // the exit status, or -1 when killed by a signal
	int signal = 0;
	bool timed_out = false;
	bool cancelled = false;  // killed because options.cancel was set
	std::string output;     // stdout and stderr together
	std::string error;
	bool ok() const { return started && exit_code == 0; }
};

struct Options {
	std::chrono::milliseconds timeout{std::chrono::seconds(15)};
	std::string input;
	std::vector<std::string> env;  // extra NAME=value entries
	bool capture = true;
	std::string dir;
	// Set from another thread: the program is killed (exec.CommandContext with a cancelled context). Checked at
	// least every 100 ms.
	const std::atomic<bool> *cancel = nullptr;
};

Result run(const std::vector<std::string> &argv, const Options &options = {});

// In a forked child, before exec: every descriptor above stderr closes at the exec. The server opens its sockets
// and device and video-memory descriptors without close-on-exec; a daemon started from a command would keep them
// (port 443, video memory) after the server is gone. Async-signal-safe.
void cloexec_inherited();

} // namespace nkos::process
