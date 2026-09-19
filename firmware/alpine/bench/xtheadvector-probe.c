#include <inttypes.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>

/* GCC exposes the C906 instruction set through explicit XTheadVector
 * intrinsics. Keeping this path conditional lets the same source produce a
 * scalar ABI-compatible binary without accidentally enabling standard RVV. */
#if defined(__riscv_xtheadvector)
#include <riscv_th_vector.h>
#endif

#define ELEMENTS (1u << 20)

static uint32_t input[ELEMENTS];
static uint32_t output[ELEMENTS];

__attribute__((noinline))
static void mix_words(const uint32_t *restrict source,
                      uint32_t *restrict destination,
                      size_t count)
{
#if defined(__riscv_xtheadvector)
    for (size_t index = 0; index < count;) {
        size_t vl = __riscv_vsetvl_e32m1(count - index);
        vuint32m1_t values = __riscv_vle32_v_u32m1(source + index, vl);
        values = __riscv_vadd_vx_u32m1(values, UINT32_C(0x7f4a7c15), vl);
        __riscv_vse32_v_u32m1(destination + index, values, vl);
        index += vl;
    }
#else
    for (size_t index = 0; index < count; ++index) {
        uint32_t value = source[index];
        destination[index] = value + UINT32_C(0x7f4a7c15);
    }
#endif
}

__attribute__((noinline))
static uint32_t checksum_words(const uint32_t *source, size_t count)
{
    uint32_t checksum = UINT32_C(0x9e3779b9);

    for (size_t index = 0; index < count; ++index)
        checksum ^= source[index] + (checksum << 6) + (checksum >> 2);

    return checksum;
}

int main(void)
{
    for (size_t index = 0; index < ELEMENTS; ++index)
        input[index] = (uint32_t)index * UINT32_C(0x2545f491) + UINT32_C(0xd1b54a32);

    mix_words(input, output, ELEMENTS);
    uint32_t checksum = checksum_words(output, ELEMENTS);
    printf("elements=%zu checksum=%" PRIx32 "\n", (size_t)ELEMENTS, checksum);
    return 0;
}
