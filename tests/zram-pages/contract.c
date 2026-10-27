/* SPDX-License-Identifier: GPL-2.0-only */
/* Included after the actual zram helpers extracted from the patched source. */
#include <sys/mman.h>
#include <stdio.h>
static unsigned long checks;
#define CHECK(x)                                                                                   \
    do {                                                                                           \
        checks++;                                                                                  \
        if (!(x)) {                                                                                \
            fprintf(stderr, "FAIL line %d: %s\n", __LINE__, #x);                                   \
            exit(1);                                                                               \
        }                                                                                          \
    } while (0)
int main(void)
{
    const unsigned long values[] = {
        0, ~0UL, 0x0102030405060708UL, 0xa5a5a5a5a5a5a5a5UL, 0x8000000000000000UL, 1};
    unsigned char *mapping =
        mmap(NULL, 4 * PAGE_SIZE, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    CHECK(mapping != MAP_FAILED);
    CHECK(!mprotect(mapping + PAGE_SIZE, 2 * PAGE_SIZE, PROT_READ | PROT_WRITE));
    unsigned char *end = mapping + 3 * PAGE_SIZE;
    for (unsigned v = 0; v < ARRAY_SIZE(values); v++) {
        /* All supported lengths, with an inaccessible page immediately after the
         * destination. Word-aligned pointers exercise both pair alignments. */
        for (size_t len = 0; len <= PAGE_SIZE; len += sizeof(unsigned long)) {
            unsigned char *start = end - len;
            memset(start - 16, 0x7a, len + 16);
            zram_fill_page(start, len, values[v]);
            for (unsigned i = 0; i < len / sizeof(unsigned long); i++)
                CHECK(((unsigned long *)start)[i] == values[v]);
            for (unsigned i = 1; i <= 16; i++)
                CHECK(start[-(int)i] == 0x7a);
        }
    }
    CHECK(!munmap(mapping, 4 * PAGE_SIZE));
    printf("PASS checks=%lu fill_lengths=513 patterns=6\n", checks);
    return 0;
}
