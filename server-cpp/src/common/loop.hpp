#pragma once

// Work off and back onto the event loop. The loop thread runs every request handler; anything slow (password
// hashing, files, commands) goes to the worker thread, and the result comes back with post(). Delays use the
// loop's timers, never a sleep.

#include <atomic>
#include <chrono>
#include <functional>

struct lws_context;

namespace nkos::loop {

// Called once by the server with its context; post() and after() are usable from then on.
void attach(lws_context *context);
void detach();

// Runs fn on the loop thread, soon. Safe from any thread.
void post(std::function<void()> fn);

// Runs fn on the loop thread after delay. Loop thread only. Dropped once the loop is detached.
void after(std::chrono::milliseconds delay, std::function<void()> fn);

// Runs fn on the worker thread (one, in order). Safe from any thread.
void work(std::function<void()> fn);

// Runs fn on a pool thread (four, more while all are busy) for blocking work (external commands, slow files), in no particular order
// between them. Safe from any thread.
void blocking(std::function<void()> fn);

// Whether the server is stopping: the loop no longer serves connections and waits for the jobs still running. A
// long job checks this to end early and release what it holds. Safe from any thread.
bool stopping();
// The same as a flag, for process::Options::cancel: a program run under it is killed when the server stops.
const std::atomic<bool> *stop_flag();

// Runs fn on the device thread (one, in order): native device calls that can take long (HDMI capture on/off takes
// about 0.3 s), apart from the worker so a password hash does not hold them up. Safe from any thread.
void device(std::function<void()> fn);

// work, blocking and device run fn on the calling thread once the loop is detached (the server is stopping).

// The loop calls this when lws_cancel_service woke it: runs the posted functions.
void drain();

} // namespace nkos::loop
