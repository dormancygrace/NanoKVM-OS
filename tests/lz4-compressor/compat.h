/* Native kernel-API shim. Production builds use the real Linux headers. */
#ifndef TEST_LZ4_COMPAT_H
#define TEST_LZ4_COMPAT_H
#include <limits.h>
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
typedef uint8_t u8;
typedef uint16_t u16;
typedef uint32_t u32;
typedef int32_t s32;
typedef uint64_t u64;
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif
#define static_assert _Static_assert
#define likely(x) __builtin_expect(!!(x), 1)
#define unlikely(x) __builtin_expect(!!(x), 0)
#define EXPORT_SYMBOL(x)
#define MODULE_LICENSE(x)
#define MODULE_DESCRIPTION(x)
#define LZ4_MEMORY_USAGE 14
#define LZ4_MEM_COMPRESS ((1U << LZ4_MEMORY_USAGE) + 32)
#define LZ4_MAX_INPUT_SIZE 0x7E000000
#define LZ4_COMPRESSBOUND(n) ((unsigned)(n) > LZ4_MAX_INPUT_SIZE ? 0 : (n) + (n) / 255 + 16)
#define LZ4_ACCELERATION_DEFAULT 1
typedef union {
    u64 alignment;
    u8 storage[LZ4_MEM_COMPRESS];
} LZ4_stream_t;
static inline int LZ4_compressBound(size_t n) { return LZ4_COMPRESSBOUND(n); }
int LZ4_compress_fast(const char *, char *, int, int, int, void *);
int LZ4_compress_default(const char *, char *, int, int, void *);
int LZ4_compress_destSize(const char *, char *, int *, int, void *);
void LZ4_resetStream(LZ4_stream_t *);
int LZ4_loadDict(LZ4_stream_t *, const char *, int);
int LZ4_saveDict(LZ4_stream_t *, char *, int);
int LZ4_compress_fast_continue(LZ4_stream_t *, const char *, char *, int, int, int);
#endif
