#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

#define WORDS (1u << 20)

static uint64_t *values;

static inline uint64_t rotl64(uint64_t value, unsigned count)
{
    return (value << count) | (value >> (64u - count));
}

__attribute__((noinline))
static uint64_t scalar_mix(unsigned rounds)
{
    uint64_t state = UINT64_C(0x243f6a8885a308d3);
    for (unsigned round = 0; round < rounds; ++round) {
        for (size_t index = 0; index < WORDS; ++index) {
            uint64_t value = values[index];
            state ^= value + UINT64_C(0x9e3779b97f4a7c15);
            state = rotl64(state, 17);
            state += (value ^ (value >> 23)) * UINT64_C(0xbf58476d1ce4e5b9);
            values[index] = state ^ rotl64(value, 31);
        }
    }
    return state;
}

static uint64_t elapsed_ns(struct timespec start, struct timespec end)
{
    return (uint64_t)(end.tv_sec - start.tv_sec) * UINT64_C(1000000000)
         + (uint64_t)(end.tv_nsec - start.tv_nsec);
}

int main(int argc, char **argv)
{
    unsigned rounds = 16;
    if (argc == 2) {
        char *end = NULL;
        unsigned long parsed = strtoul(argv[1], &end, 10);
        if (end == argv[1] || *end != '\0' || parsed == 0 || parsed > 1024) {
            fprintf(stderr, "invalid rounds\n");
            return 2;
        }
        rounds = (unsigned)parsed;
    }

    values = aligned_alloc(64, WORDS * sizeof(*values));
    if (values == NULL) {
        perror("aligned_alloc");
        return 1;
    }
    uint64_t seed = UINT64_C(0xd1b54a32d192ed03);
    for (size_t index = 0; index < WORDS; ++index) {
        seed ^= seed >> 12;
        seed ^= seed << 25;
        seed ^= seed >> 27;
        values[index] = seed * UINT64_C(0x2545f4914f6cdd1d);
    }

    struct timespec start;
    struct timespec end;
    if (clock_gettime(CLOCK_MONOTONIC, &start) != 0) {
        perror("clock_gettime");
        return 1;
    }
    uint64_t checksum = scalar_mix(rounds);
    if (clock_gettime(CLOCK_MONOTONIC, &end) != 0) {
        perror("clock_gettime");
        return 1;
    }

    printf("rounds=%u bytes=%zu ns=%" PRIu64 " checksum=%016" PRIx64 "\n",
           rounds, (size_t)WORDS * sizeof(*values), elapsed_ns(start, end),
           checksum);
    free(values);
    return 0;
}
