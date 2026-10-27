/* SPDX-License-Identifier: GPL-2.0-only */
#pragma once
/* Minimal native Linux compatibility for the real 64-bit little-endian codec. */
#include <errno.h>
#include <asm-generic/errno.h>
#include <assert.h>
#include <limits.h>
#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

/* linux/types.h */
typedef uint8_t u8;
typedef uint16_t u16;
typedef uint32_t u32;
typedef uint64_t u64;
typedef int8_t s8;
typedef int16_t s16;
typedef int32_t s32;
typedef int64_t s64;
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif
#define __aligned(x) __attribute__((aligned(x)))

/* linux/kernel.h */
#define likely(x) __builtin_expect(!!(x), 1)
#define unlikely(x) __builtin_expect(!!(x), 0)
#define min(a, b) ((a) < (b) ? (a) : (b))
#define max(a, b) ((a) > (b) ? (a) : (b))
#define BUILD_BUG_ON(x) _Static_assert(!(x), "BUILD_BUG_ON")
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define BUG_ON(x) assert(!(x))
#define fallthrough __attribute__((fallthrough))
#define U32_MAX UINT32_MAX
#define U64_MAX UINT64_MAX

/* linux/unaligned.h */
#define get_unaligned(p)                                                                           \
    ({                                                                                             \
        __typeof__(*(p)) _v;                                                                       \
        memcpy((void *)&_v, (p), sizeof(_v));                                                      \
        _v;                                                                                        \
    })
#define put_unaligned(v, p)                                                                        \
    do {                                                                                           \
        __typeof__(*(p)) _v = (v);                                                                 \
        memcpy((p), &_v, sizeof(_v));                                                              \
    } while (0)
static inline uint16_t get_unaligned_le16(const void *p)
{
    uint16_t v;
    memcpy(&v, p, 2);
    return v;
}
static inline void put_unaligned_le16(uint16_t v, void *p) { memcpy(p, &v, 2); }
static inline uint32_t get_unaligned_le32(const void *p)
{
    uint32_t v;
    memcpy(&v, p, 4);
    return v;
}
static inline void put_unaligned_le32(uint32_t v, void *p) { memcpy(p, &v, 4); }
static inline uint32_t get_unaligned_be32(const void *p)
{
    uint32_t v;
    memcpy(&v, p, 4);
    return __builtin_bswap32(v);
}
static inline void put_unaligned_be32(uint32_t v, void *p)
{
    v = __builtin_bswap32(v);
    memcpy(p, &v, 4);
}
static inline uint64_t get_unaligned_le64(const void *p)
{
    uint64_t v;
    memcpy(&v, p, 8);
    return v;
}
static inline void put_unaligned_le64(uint64_t v, void *p) { memcpy(p, &v, 8); }
static inline uint64_t get_unaligned_be64(const void *p)
{
    uint64_t v;
    memcpy(&v, p, 8);
    return __builtin_bswap64(v);
}
static inline void put_unaligned_be64(uint64_t v, void *p)
{
    v = __builtin_bswap64(v);
    memcpy(p, &v, 8);
}

/* linux/bitops.h */
#define BITS_PER_LONG (__SIZEOF_LONG__ * CHAR_BIT)
static inline unsigned long __ffs(unsigned long x) { return __builtin_ctzl(x); }
static inline unsigned long __fls(unsigned long x) { return BITS_PER_LONG - 1 - __builtin_clzl(x); }
static inline unsigned rol32(unsigned x, unsigned n) { return (x << n) | (x >> (32 - n)); }
static inline unsigned long long rol64(unsigned long long x, unsigned n)
{
    return (x << n) | (x >> (64 - n));
}

/* linux/compiler.h */
#define __packed __attribute__((packed))
#define __maybe_unused __attribute__((unused))
#define noinline __attribute__((noinline))
#define barrier() __asm__ __volatile__("" ::: "memory")

/* linux/swab.h */
#define swab32(x) __builtin_bswap32(x)
#define swab64(x) __builtin_bswap64(x)

/* linux/module.h */
#define EXPORT_SYMBOL(x)
#define MODULE_LICENSE(x)
#define MODULE_DESCRIPTION(x)
#define EXPORT_SYMBOL_GPL(x)
#define MODULE_AUTHOR(x)

/* linux/math64.h */
static inline uint64_t div_u64(uint64_t a, uint32_t b) { return a / b; }

/* linux/printk.h */
#define pr_debug(...)                                                                              \
    do {                                                                                           \
    } while (0)
