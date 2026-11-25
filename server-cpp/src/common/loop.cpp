#include "common/loop.hpp"

#include <libwebsockets.h>

#include <atomic>
#include <condition_variable>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <thread>
#include <vector>

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
	int idle = 0;  // threads waiting in run(), under mutex
	std::thread thread;
	std::atomic<bool> exited{false};

	void run()
	{
		for (;;) {
			std::function<void()> job;
			{
				std::unique_lock lock(mutex);
				idle++;
				wake.wait(lock, [&] { return stop || !jobs.empty(); });
				idle--;
				if (jobs.empty()) return;
				job = std::move(jobs.front());
				jobs.pop_front();
			}
			job();
		}
	}
};
Worker *worker;
Worker *device_worker;
std::vector<Worker *> pool;
// The pool starts with four threads and grows while every thread is busy and a job waits: a script run, an ATX
// press, a download or a package installation each keep a thread for a long time, and the short jobs of the other
// requests must not wait behind them. The Go server had a goroutine for each request. The added threads stay; they
// are counted and listed under the shared queue's mutex.
constexpr std::size_t pool_start = 4, pool_max = 32;
std::size_t pool_threads = 0;
std::vector<Worker *> pool_extra;

} // namespace

void drain();

namespace {

Worker *pool_thread(Worker *shared)
{
	auto *w = new Worker;
	w->thread = std::thread([shared, w] {
		shared->run();
		w->exited = true;
	});
	return w;
}

// Joins a worker's thread and keeps running what is posted to the loop meanwhile: a job still finishing there may
// post to the loop and wait for the result (usb's stop_audio), and the loop is the thread that joins.
void join_draining(Worker *w)
{
	while (!w->exited) {
		drain();
		std::this_thread::sleep_for(std::chrono::milliseconds(2));
	}
	w->thread.join();
}

} // namespace

void attach(lws_context *context)
{
	ctx = context;
	worker = new Worker;
	worker->thread = std::thread([w = worker] {
		w->run();
		w->exited = true;
	});
	device_worker = new Worker;
	device_worker->thread = std::thread([w = device_worker] {
		w->run();
		w->exited = true;
	});
	// One shared queue for the pool: the first idle thread takes the next job.
	auto *shared = new Worker;
	pool.push_back(shared);
	for (std::size_t i = 0; i < pool_start; i++) pool.push_back(pool_thread(shared));
	pool_threads = pool_start;
	// Functions posted before the loop existed run at its first turn.
	lws_cancel_service(ctx);
}

void detach()
{
	if (!pool.empty()) {
		{
			std::lock_guard lock(pool[0]->mutex);
			pool[0]->stop = true;
		}
		pool[0]->wake.notify_all();
		for (std::size_t i = 1; i < pool.size(); i++) {
			join_draining(pool[i]);
			delete pool[i];
		}
		std::vector<Worker *> extra;
		{
			// stop is set: blocking() adds no thread from here on.
			std::lock_guard lock(pool[0]->mutex);
			extra.swap(pool_extra);
		}
		for (Worker *w : extra) {
			join_draining(w);
			delete w;
		}
		delete pool[0];
		pool.clear();
	}
	for (Worker **w : {&worker, &device_worker}) {
		if (!*w) continue;
		{
			std::lock_guard lock((*w)->mutex);
			(*w)->stop = true;
		}
		(*w)->wake.notify_all();
		join_draining(*w);
		delete *w;
		*w = nullptr;
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
	// After detach (lws_context_destroy closing the last connections): no loop to fire it.
	if (!ctx) return;
	auto node = std::make_unique<lws_sorted_usec_list_t>();
	lws_sorted_usec_list_t *sul = node.get();
	*sul = {};
	timers.emplace(sul, std::pair{std::move(node), std::move(fn)});
	lws_sul_schedule(ctx, 0, sul, fire, static_cast<lws_usec_t>(delay.count()) * LWS_US_PER_MS);
}

void work(std::function<void()> fn)
{
	if (!worker) return fn();
	{
		std::lock_guard lock(worker->mutex);
		worker->jobs.push_back(std::move(fn));
	}
	worker->wake.notify_one();
}

void blocking(std::function<void()> fn)
{
	if (pool.empty()) return fn();
	{
		std::lock_guard lock(pool[0]->mutex);
		pool[0]->jobs.push_back(std::move(fn));
		if (pool[0]->jobs.size() > static_cast<std::size_t>(pool[0]->idle) && pool_threads < pool_max && !pool[0]->stop) {
			pool_threads++;
			pool_extra.push_back(pool_thread(pool[0]));
		}
	}
	pool[0]->wake.notify_one();
}

void device(std::function<void()> fn)
{
	if (!device_worker) return fn();
	{
		std::lock_guard lock(device_worker->mutex);
		device_worker->jobs.push_back(std::move(fn));
	}
	device_worker->wake.notify_one();
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
