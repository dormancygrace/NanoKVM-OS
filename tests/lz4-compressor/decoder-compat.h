/* Native adapters for the real Linux decoder and public LZ4 header. */
#pragma once
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
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

#include <stdlib.h>
#define likely(x) __builtin_expect(!!(x), 1)
#define unlikely(x) __builtin_expect(!!(x), 0)
#define min(a, b) ((a) < (b) ? (a) : (b))
#define max(a, b) ((a) > (b) ? (a) : (b))
#define BUILD_BUG_ON(x) _Static_assert(!(x), "BUILD_BUG_ON")
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))

#include <limits.h>
#define BITS_PER_LONG (__SIZEOF_LONG__ * CHAR_BIT)
static inline unsigned long __ffs(unsigned long x) { return __builtin_ctzl(x); }
static inline unsigned long __fls(unsigned long x) { return BITS_PER_LONG - 1 - __builtin_clzl(x); }

#include <string.h>
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
static inline uint16_t get_unaligned_le16(const void *p) {
    uint16_t v;
    memcpy(&v, p, 2);
    return v;
}
static inline void put_unaligned_le16(uint16_t v, void *p) { memcpy(p, &v, 2); }

#define EXPORT_SYMBOL(x)
#define MODULE_LICENSE(x)
#define MODULE_DESCRIPTION(x)
