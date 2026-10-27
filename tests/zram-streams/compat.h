/* SPDX-License-Identifier: GPL-2.0-only */
#pragma once
#include "../kernel-zstd/compat.h"
#include <pthread.h>
#include <stdatomic.h>
#include <stdarg.h>
#include <stdio.h>
#include <sys/types.h>
#define PAGE_SIZE 4096
#define __percpu
#define GFP_NOIO 0
#define __GFP_NOWARN 0
#define CONFIG_ZRAM_BACKEND_LZ4 1
#define CONFIG_ZRAM_BACKEND_LZO 0
#define CONFIG_ZRAM_BACKEND_LZ4HC 0
#define CONFIG_ZRAM_BACKEND_ZSTD 0
#define CONFIG_ZRAM_BACKEND_DEFLATE 0
#define CONFIG_ZRAM_BACKEND_842 0
#define IS_ENABLED(x) (x)
#define CPUHP_ZCOMP_PREPARE 1
struct hlist_node {
    void *next, *prev;
};
struct mutex {
    pthread_mutex_t native;
};
void host_mutex_lock(struct mutex *lock);
void *host_alloc(size_t size);
void host_free(void *address);
unsigned host_current_cpu(void);
int cpuhp_state_add_instance(int state, struct hlist_node *node);
void cpuhp_state_remove_instance(int state, struct hlist_node *node);
#define mutex_init(m) assert(!pthread_mutex_init(&(m)->native, NULL))
#define mutex_lock(m) host_mutex_lock(m)
#define mutex_unlock(m) assert(!pthread_mutex_unlock(&(m)->native))
#define kzalloc_obj(x) host_alloc(sizeof(x))
#define vzalloc(n) host_alloc(n)
#define kfree(p) host_free(p)
#define vfree(p) host_free(p)
#define alloc_percpu(t) host_alloc(2 * sizeof(t))
#define free_percpu(p) host_free(p)
#define per_cpu_ptr(p, cpu) ((p) + (cpu))
#define raw_cpu_ptr(p) per_cpu_ptr(p, host_current_cpu())
#define for_each_possible_cpu(cpu) for ((cpu) = 0; (cpu) < 2; (cpu)++)
#define hlist_entry(p, t, member) ((t *)((char *)(p) - offsetof(t, member)))
#define ERR_PTR(x) ((void *)(intptr_t)(x))
#define IS_ERR(x) ((uintptr_t)(x) >= (uintptr_t)-4095)
#define might_sleep()                                                                              \
    do {                                                                                           \
    } while (0)
#define pr_err(...)                                                                                \
    do {                                                                                           \
    } while (0)
static inline bool sysfs_streq(const char *a, const char *b)
{
    return strcspn(a, "\n") == strcspn(b, "\n") && !strncmp(a, b, strcspn(a, "\n"));
}
static inline int sysfs_emit_at(char *out, ssize_t at, const char *fmt, ...)
{
    va_list args;
    va_start(args, fmt);
    int n = vsnprintf(out + at, PAGE_SIZE - at, fmt, args);
    va_end(args);
    return n;
}
