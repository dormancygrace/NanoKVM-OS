/* SPDX-License-Identifier: GPL-2.0-only */
#pragma once
#include "../kernel-zstd/compat.h"
#include <sys/types.h>
#define PAGE_SIZE 4096
#define GFP_NOIO 0
#define __GFP_NOWARN 0
#define __percpu
struct hlist_node {
    void *next, *prev;
};
struct mutex {
    int unused;
};
void *host_alloc(size_t size);
void host_free(void *address);
#define kzalloc_obj(x) host_alloc(sizeof(x))
#define kvzalloc(n, f) host_alloc(n)
#define vzalloc(n) host_alloc(n)
#define kfree(p) host_free(p)
#define kvfree(p) host_free(p)
#define vfree(p) host_free(p)
#define pr_err(...)                                                                                \
    do {                                                                                           \
    } while (0)
