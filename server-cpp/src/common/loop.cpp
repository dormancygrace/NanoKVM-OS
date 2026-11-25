#include "common/loop.hpp"

#include <libwebsockets.h>

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <deque>
#include <map>
#include <memory>
#include <mutex>
#include <new>
#include <system_error>
#include <thread>
#include <vector>

namespace nkos::loop {

namespace {

lws_context *ctx;
std::mutex posted_mutex;
std::deque<std::function<void()>> posted;

// A timer's list node lives in its own allocation, so its address is the key to the function. due is kept for the
// time after the loop stopped serving, when lws fires nothing any more (run_due_timers).
struct Timer {
	std::unique_ptr<lws_sorted_usec_list_t> node;
	std::function<void()> fn;
	std::chrono::steady_clock::time_point due;
};
std::map<lws_sorted_usec_list_t *, Timer> timers;

void fire(lws_sorted_usec_list_t *sul)
{
	auto it = timers.find(sul);
	if (it == timers.end()) return;
	auto fn = std::move(it->second.fn);
	timers.erase(it);
	fn();
}

// The timers that are due, in order of their time, while the loop itself no longer serves: a job still finishing on
// a worker thread may wait for one (a screen read waits for the HDMI input to settle, or for its retry).
void run_due_timers()
{
	for (;;) {
		auto now = std::chrono::steady_clock::now();
		auto next = timers.end();
		for (auto it = timers.begin(); it != timers.end(); ++it)
			if (it->second.due <= now && (next == timers.end() || it->second.due < next->second.due)) next = it;
		if (next == timers.end()) return;
		lws_sul_cancel(next->first);
		auto fn = std::move(next->second.fn);
		timers.erase(next);
		fn();
	}
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
std::atomic<bool> detaching{false};
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

// nullptr when the system gives no more threads: the pool goes on with the ones it has.
Worker *pool_thread(Worker *shared)
{
	auto *w = new Worker;
	try {
		w->thread = std::thread([shared, w] {
			shared->run();
			w->exited = true;
		});
	} catch (const std::system_error &) {
		delete w;
		return nullptr;
	}
	return w;
}

// Joins a worker's thread and keeps doing the loop's part meanwhile: a job still finishing there may post to the
// loop and wait for the result (usb's stop_audio), or wait for a timer (a screen read), and the loop is the thread
// that joins.
void join_draining(Worker *w)
{
	while (!w->exited) {
		drain();
		run_due_timers();
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
	// The first threads are required; std::thread throws if the system refuses them.
	for (std::size_t i = 0; i < pool_start; i++) {
		auto *w = new Worker;
		w->thread = std::thread([shared, w] {
			shared->run();
			w->exited = true;
		});
		pool.push_back(w);
	}
	pool_threads = pool_start;
	// Functions posted before the loop existed run at its first turn.
	lws_cancel_service(ctx);
}

namespace {

// Nothing is running or queued on w's threads.
bool quiet(Worker *w, const std::size_t &threads)
{
	std::lock_guard lock(w->mutex);
	return w->jobs.empty() && static_cast<std::size_t>(w->idle) == threads;
}

} // namespace

bool stopping() { return detaching.load(); }
const std::atomic<bool> *stop_flag() { return &detaching; }

void detach()
{
	// Long jobs look at stopping() and end early (a download in progress).
	detaching = true;
	// Every thread stays available until all of them are idle and nothing is posted: a job still running may need
	// another one to finish (a screen read waits on a pool thread for the recognition it queues on the pool), a
	// posted function or a timer. Only then are they told to stop; a thread told to stop while a job still ran
	// elsewhere would leave that job's follow-up without a worker.
	const std::size_t one = 1;
	for (;;) {
		drain();
		run_due_timers();
		bool posted_empty;
		{
			std::lock_guard lock(posted_mutex);
			posted_empty = posted.empty();
		}
		if (posted_empty && (pool.empty() || quiet(pool[0], pool_threads)) && (!worker || quiet(worker, one)) &&
		    (!device_worker || quiet(device_worker, one)))
			break;
		std::this_thread::sleep_for(std::chrono::milliseconds(2));
	}
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
	timers.emplace(sul, Timer{std::move(node), std::move(fn), std::chrono::steady_clock::now() + delay});
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
		std::unique_lock lock(pool[0]->mutex);
		// The pool is stopping (the loop is detaching and its threads may be gone): on the caller, as after it.
		if (pool[0]->stop) {
			lock.unlock();
			return fn();
		}
		pool[0]->jobs.push_back(std::move(fn));
		if (pool[0]->jobs.size() > static_cast<std::size_t>(pool[0]->idle) && pool_threads < pool_max && !pool[0]->stop) {
			// Growth is optional: without room for the record or without a thread the job waits for the
			// existing ones, and is woken below either way.
			try {
				pool_extra.reserve(pool_extra.size() + 1);
				if (Worker *w = pool_thread(pool[0])) {
					pool_extra.push_back(w);
					pool_threads++;
				}
			} catch (const std::bad_alloc &) {
			}
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
