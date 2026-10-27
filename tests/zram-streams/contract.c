/* SPDX-License-Identifier: GPL-2.0-only */
/* Test the actual zcomp frontend with deterministic backend and CPUHP shims.
 * Codec correctness and target scheduling are checked separately. */
#include <time.h>
#include "zcomp.h"
static atomic_ulong checks, blocked, allocations, live, fail_at, destroyed;
static bool online[2];
static _Thread_local unsigned cpu_reads;
static _Thread_local bool migrate;
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        atomic_fetch_add(&checks, 1);                                                              \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL line %d: %s\n", __LINE__, #x);                                   \
            abort();                                                                               \
        }                                                                                          \
    } while (0)
static void wait_at_least(atomic_ulong *value, unsigned long target)
{
    struct timespec start, now, pause = {.tv_nsec = 1000000};
    clock_gettime(CLOCK_MONOTONIC, &start);
    while (atomic_load(value) < target) {
        nanosleep(&pause, NULL);
        clock_gettime(CLOCK_MONOTONIC, &now);
        CHECK(now.tv_sec - start.tv_sec < 5);
    }
}
void host_mutex_lock(struct mutex *lock)
{
    int err = pthread_mutex_trylock(&lock->native);
    if (err == EBUSY) {
        atomic_fetch_add(&blocked, 1);
        err = pthread_mutex_lock(&lock->native);
    }
    CHECK(!err);
}
void *host_alloc(size_t size)
{
    unsigned long call = atomic_fetch_add(&allocations, 1) + 1;
    if (call == atomic_load(&fail_at))
        return NULL;
    void *p = calloc(1, size);
    if (p)
        atomic_fetch_add(&live, 1);
    return p;
}
void host_free(void *address)
{
    if (address) {
        CHECK(atomic_fetch_sub(&live, 1) > 0);
        free(address);
    }
}
unsigned host_current_cpu(void) { return migrate && cpu_reads++ ? 1 : 0; }
int cpuhp_state_add_instance(int state, struct hlist_node *node)
{
    CHECK(state == CPUHP_ZCOMP_PREPARE);
    memset(online, 0, sizeof(online));
    for (unsigned cpu = 0; cpu < 2; cpu++) {
        int err = zcomp_cpu_up_prepare(cpu, node);
        if (err) {
            while (cpu)
                zcomp_cpu_dead(--cpu, node);
            return err;
        }
        online[cpu] = true;
    }
    return 0;
}
void cpuhp_state_remove_instance(int state, struct hlist_node *node)
{
    CHECK(state == CPUHP_ZCOMP_PREPARE);
    for (unsigned cpu = 0; cpu < 2; cpu++)
        if (online[cpu]) {
            zcomp_cpu_dead(cpu, node);
            online[cpu] = false;
        }
}
static int setup(struct zcomp_params *params)
{
    params->drv_data = host_alloc(32);
    return params->drv_data ? 0 : -ENOMEM;
}
static void release(struct zcomp_params *params)
{
    host_free(params->drv_data);
    params->drv_data = NULL;
}
static int create(struct zcomp_params *params, struct zcomp_ctx *ctx)
{
    (void)params;
    ctx->context = host_alloc(2 * sizeof(unsigned long));
    return ctx->context ? 0 : -ENOMEM;
}
static void destroy(struct zcomp_ctx *ctx)
{
    if (ctx->context) {
        atomic_fetch_add(&destroyed, 1);
        host_free(ctx->context);
        ctx->context = NULL;
    }
}
static int compress(struct zcomp_params *params, struct zcomp_ctx *ctx, struct zcomp_req *req)
{
    (void)params;
    CHECK(req->src_len == PAGE_SIZE && req->dst_len >= PAGE_SIZE);
    ((unsigned long *)ctx->context)[0]++;
    memcpy(req->dst, req->src, PAGE_SIZE);
    req->dst_len = PAGE_SIZE;
    return 0;
}
static int decompress(struct zcomp_params *params, struct zcomp_ctx *ctx, struct zcomp_req *req)
{
    (void)params;
    CHECK(req->src_len == PAGE_SIZE && req->dst_len == PAGE_SIZE);
    ((unsigned long *)ctx->context)[1]++;
    memcpy(req->dst, req->src, PAGE_SIZE);
    return 0;
}
const struct zcomp_ops backend_lz4 = {.name = "lz4",
                                      .setup_params = setup,
                                      .release_params = release,
                                      .create_ctx = create,
                                      .destroy_ctx = destroy,
                                      .compress = compress,
                                      .decompress = decompress};
struct task {
    struct zcomp *comp;
    bool read, teardown;
    unsigned iterations;
    atomic_ulong done;
};
static void *work(void *opaque)
{
    struct task *task = opaque;
    if (task->teardown)
        zcomp_destroy(task->comp);
    else {
        unsigned char page[PAGE_SIZE], out[PAGE_SIZE];
        memset(page, task->read ? 0x5a : 0xa5, sizeof(page));
        for (unsigned i = 0; i < task->iterations; i++) {
            struct zcomp_strm *stream =
                task->read ? zcomp_dstream_get(task->comp) : zcomp_stream_get(task->comp);
            if (task->read) {
                CHECK(!zcomp_decompress(task->comp, stream, page, sizeof(page), out));
                CHECK(!memcmp(page, out, sizeof(page)));
                zcomp_dstream_put(stream);
            } else {
                unsigned size;
                CHECK(!zcomp_compress(task->comp, stream, page, &size));
                CHECK(size == PAGE_SIZE && !memcmp(page, stream->buffer, size));
                zcomp_stream_put(stream);
            }
        }
    }
    atomic_store(&task->done, 1);
    return NULL;
}
static void direction_tests(void)
{
    struct zcomp_params params = {0};
    struct zcomp *comp = zcomp_create("lz4", &params);
    CHECK(!IS_ERR(comp));
    for (unsigned held = 0; held < 2; held++)
        for (unsigned other = 0; other < 2; other++) {
            struct zcomp_strm *stream = held ? zcomp_dstream_get(comp) : zcomp_stream_get(comp);
            unsigned long before = atomic_load(&blocked);
            struct task task = {.comp = comp, .read = other, .iterations = 1};
            pthread_t thread;
            CHECK(!pthread_create(&thread, NULL, work, &task));
            if (held != other)
                wait_at_least(&task.done, 1);
            else {
                wait_at_least(&blocked, before + 1);
                CHECK(!atomic_load(&task.done));
            }
            if (held)
                zcomp_dstream_put(stream);
            else
                zcomp_stream_put(stream);
            wait_at_least(&task.done, 1);
            CHECK(!pthread_join(thread, NULL));
        }
    struct task tasks[4];
    pthread_t threads[4];
    for (unsigned i = 0; i < 4; i++) {
        tasks[i] = (struct task){.comp = comp, .read = i & 1, .iterations = 500};
        CHECK(!pthread_create(threads + i, NULL, work, tasks + i));
    }
    for (unsigned i = 0; i < 4; i++) {
        wait_at_least(&tasks[i].done, 1);
        CHECK(!pthread_join(threads[i], NULL));
    }
    unsigned long *calls = comp->stream[0].ctx.context;
    CHECK(calls[0] == 1002 && calls[1] == 1002);
    zcomp_destroy(comp);
    CHECK(!atomic_load(&live));
}
static void teardown_tests(void)
{
    for (unsigned both = 0; both < 2; both++) {
        struct zcomp_params params = {0};
        struct zcomp *comp = zcomp_create("lz4", &params);
        CHECK(!IS_ERR(comp));
        struct zcomp_strm *writer = both ? zcomp_stream_get(comp) : NULL,
                          *reader = zcomp_dstream_get(comp);
        unsigned long before = atomic_load(&blocked), freed = atomic_load(&destroyed);
        struct task task = {.comp = comp, .teardown = true};
        pthread_t thread;
        CHECK(!pthread_create(&thread, NULL, work, &task));
        wait_at_least(&blocked, before + 1);
        CHECK(atomic_load(&destroyed) == freed && !atomic_load(&task.done));
        if (both) {
            zcomp_stream_put(writer);
            wait_at_least(&blocked, before + 2);
            CHECK(atomic_load(&destroyed) == freed && !atomic_load(&task.done));
        }
        zcomp_dstream_put(reader);
        wait_at_least(&task.done, 1);
        CHECK(!pthread_join(thread, NULL));
        CHECK(atomic_load(&destroyed) == freed + 2 && !atomic_load(&live));
    }
}
static void migration_tests(void)
{
    for (unsigned read = 0; read < 2; read++) {
        struct zcomp_params params = {0};
        struct zcomp *comp = zcomp_create("lz4", &params);
        CHECK(!IS_ERR(comp));
        CHECK(!zcomp_cpu_dead(0, &comp->node));
        online[0] = false;
        migrate = true;
        cpu_reads = 0;
        struct zcomp_strm *stream = read ? zcomp_dstream_get(comp) : zcomp_stream_get(comp);
        CHECK(stream == comp->stream + 1 && cpu_reads == 2);
        if (read)
            zcomp_dstream_put(stream);
        else
            zcomp_stream_put(stream);
        migrate = false;
        zcomp_destroy(comp);
        CHECK(!atomic_load(&live));
    }
}
int main(void)
{
    struct zcomp_params params = {0};
    atomic_store(&allocations, 0);
    struct zcomp *comp = zcomp_create("lz4", &params);
    CHECK(!IS_ERR(comp));
    unsigned long total = atomic_load(&allocations);
    zcomp_destroy(comp);
    CHECK(!atomic_load(&live));
    for (unsigned long failure = 1; failure <= total; failure++) {
        atomic_store(&allocations, 0);
        atomic_store(&fail_at, failure);
        comp = zcomp_create("lz4", &params);
        CHECK(IS_ERR(comp) && !atomic_load(&live));
    }
    atomic_store(&fail_at, 0);
    CHECK(IS_ERR(zcomp_create("invalid", &params)));
    CHECK(!atomic_load(&live));
    direction_tests();
    teardown_tests();
    migration_tests();
    printf("PASS checks=%lu allocation_failure_points=%lu direction_serialization=4 "
           "concurrent_calls=2000 teardown=2 migration=2\n",
           atomic_load(&checks), total);
    return 0;
}
