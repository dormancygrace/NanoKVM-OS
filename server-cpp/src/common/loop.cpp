#include "common/loop.hpp"

#include <libwebsockets.h>

#include <condition_variable>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <thread>

namespace nkos::loop {

namespace {

lws_context *ctx;
std::mutex posted_mutex;
std::deque<std::function<void()>> posted;

// A timer's list node lives in its own allocation, so its address is the key to the function.
std::map<lws_sorted_usec_list_t *, std::pair<std::unique_ptr<lws_sorted_usec_list_t>, std::function<void()>>> timers;

void fire(lws_sorted_usec_list_t *sul)
{
	auto it = timers.find(sul);
	if (it == timers.end()) return;
	auto fn = std::move(it->second.second);
	timers.erase(it);
	fn();
}

struct Worker {
	std::mutex mutex;
	std::condition_variable wake;
	std::deque<std::function<void()>> jobs;
	bool stop = false;
	std::thread thread;

	void run()
	{
		for (;;) {
			std::function<void()> job;
			{
				std::unique_lock lock(mutex);
				wake.wait(lock, [&] { return stop || !jobs.empty(); });
				if (jobs.empty()) return;
				job = std::move(jobs.front());
				jobs.pop_front();
			}
			job();
		}
	}
};
Worker *worker;

} // namespace

void attach(lws_context *context)
{
	ctx = context;
	worker = new Worker;
	worker->thread = std::thread([] { worker->run(); });
}

void detach()
{
	if (worker) {
		{
			std::lock_guard lock(worker->mutex);
			worker->stop = true;
		}
		worker->wake.notify_all();
		worker->thread.join();
		delete worker;
		worker = nullptr;
	}
	for (auto &[sul, entry] : timers) lws_sul_cancel(sul);
	timers.clear();
	ctx = nullptr;
}

void post(std::function<void()> fn)
{
	{
		std::lock_guard lock(posted_mutex);
		posted.push_back(std::move(fn));
	}
	if (ctx) lws_cancel_service(ctx);
}

void after(std::chrono::milliseconds delay, std::function<void()> fn)
{
	auto node = std::make_unique<lws_sorted_usec_list_t>();
	lws_sorted_usec_list_t *sul = node.get();
	*sul = {};
	timers.emplace(sul, std::pair{std::move(node), std::move(fn)});
	lws_sul_schedule(ctx, 0, sul, fire, static_cast<lws_usec_t>(delay.count()) * LWS_US_PER_MS);
}

void work(std::function<void()> fn)
{
	{
		std::lock_guard lock(worker->mutex);
		worker->jobs.push_back(std::move(fn));
	}
	worker->wake.notify_one();
}

void drain()
{
	std::deque<std::function<void()>> batch;
	{
		std::lock_guard lock(posted_mutex);
		batch.swap(posted);
	}
	for (auto &fn : batch) fn();
}

} // namespace nkos::loop
