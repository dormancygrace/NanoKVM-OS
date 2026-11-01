/* SPDX-License-Identifier: GPL-2.0-only */
#pragma once
/* Minimal native Linux compatibility for the zram page-fill helper. */
#include <assert.h>
#include <limits.h>
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

typedef uint8_t u8;
typedef uint16_t u16;
typedef uint32_t u32;
typedef uint64_t u64;
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif
#define likely(x) __builtin_expect(!!(x), 1)
#define unlikely(x) __builtin_expect(!!(x), 0)
#define BUILD_BUG_ON(x) _Static_assert(!(x), "BUILD_BUG_ON")
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define BUG_ON(x) assert(!(x))
#define barrier() __asm__ __volatile__("" ::: "memory")
