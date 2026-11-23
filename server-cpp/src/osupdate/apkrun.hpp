#pragma once

// The one way the server runs apk, as the Go server's internal/apkrun: every apk run but a query of the installed
// database (info -e) holds an exclusive flock on /run/nanokvm-apk.lock (shared with the update helpers), so two
// index-loading runs (33-36 MiB each on the SG2002) never overlap; a run that could not fit in memory even after
// swapping everything else out is refused before it starts; apk gets oom_score_adj 1000; index queries run at
// nice 19 in a process group of their own (killed with everything they started when given up), package
// transactions and installed-only queries at nice 0. A transaction first waits for the post-commit worker
// (nkos-apply-updates) of the previous one. Blocking: call from a worker thread.

#include <functional>
#include <chrono>
#include <cstdint>
#include <optional>
#include <string>
#include <vector>

namespace nkos::apkrun {

inline constexpr const char *lock_path = "/run/nanokvm-apk.lock";

// "another package operation is still running; try again when it has finished"
extern const char *const busy_error;

using Clock = std::chrono::steady_clock;

struct Cmd {
	std::string path;  // the apk executable
	std::vector<std::string> args;
	// The wait for another apk run; zero waits as long as the deadline allows.
	std::chrono::milliseconds lock_wait{0};
	// The run itself, counted once the lock is held; zero leaves only the deadline.
	std::chrono::milliseconds timeout{0};
	// The memory this run needs; zero uses the default repositories' 32 MiB.
	std::int64_t memory_kib = 0;
	// The whole call, the lock wait included (Go's context deadline); none when unset.
	std::optional<Clock::time_point> deadline;
	// Polled: true gives the run up (Go's context cancellation): the wait ends with "context canceled", a running
	// apk is killed.
	std::function<bool()> cancelled;
};

struct Result {
	bool not_started = false;  // apk never ran: error says why (and is the output of combined_output)
	bool ok = false;           // ran and exited with 0
	std::string error;         // Go's error text: "exit status 1", "signal: killed", a start or not-started error
	std::string output;        // combined_output: stdout and stderr; output_split: stdout
	std::string stderr_output; // output_split: stderr (with the reason a run did not start)
	bool cancelled = false;    // ended by cancel (not by the deadline)
};

// Cmd.CombinedOutput: stdout and stderr together; for a run that did not start, the output is the reason.
Result combined_output(const Cmd &cmd);

// Cmd.Run with stdout and stderr those of the server (an operation log when the caller redirected them); the reason
// a run did not start is written to stderr.
Result run(const Cmd &cmd);

// Cmd.Run with neither stdout nor stderr (Go's nil: /dev/null); the reason a run did not start is not written.
Result run_quiet(const Cmd &cmd);

// Cmd.Run with stdout and stderr into separate buffers (output, stderr_output).
Result output_split(const Cmd &cmd);

} // namespace nkos::apkrun
