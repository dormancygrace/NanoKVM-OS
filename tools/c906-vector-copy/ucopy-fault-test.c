// SPDX-License-Identifier: GPL-2.0-only
/* Exercise copy_to_user/copy_from_user with mismatched word alignment, the
 * case the xtheadvector backend takes above its threshold, and a store fault
 * in the middle of a vector chunk (destination ends before a PROT_NONE page).
 * Uses a regular file in /tmp (tmpfs): read() copies to user, write() from it.
 */
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#define PAGE 4096UL

static int fail;
#define CHECK(c, ...) do { if (!(c)) { printf("FAIL " __VA_ARGS__); printf("\n"); fail++; } } while (0)

int main(void)
{
	const size_t sizes[] = { 16384, 16385, 20000, 65536, 65537, 262144 + 3 };
	const char *path = "/tmp/ucopy-test.bin";
	size_t max = 262144 + 64;
	unsigned char *src = malloc(max + 16), *dst = malloc(max + 16);
	int fd, runs = 0;

	for (size_t i = 0; i < max + 16; i++)
		src[i] = (unsigned char)(i * 131 + (i >> 9));
	fd = open(path, O_RDWR | O_CREAT | O_TRUNC, 0600);
	if (fd < 0) { perror("open"); return 2; }

	for (unsigned s = 0; s < sizeof(sizes) / sizeof(sizes[0]); s++) {
		for (int so = 0; so < 8; so++) {
			for (int doff = 0; doff < 8; doff++) {
				size_t n = sizes[s];
				/* copy_from_user: write() from src+so, read back aligned */
				if (pwrite(fd, src + so, n, 0) != (ssize_t)n) { perror("pwrite"); return 2; }
				memset(dst, 0, max + 16);
				/* copy_to_user: read() into dst+doff */
				if (pread(fd, dst + doff, n, 0) != (ssize_t)n) { perror("pread"); return 2; }
				CHECK(!memcmp(dst + doff, src + so, n), "size=%zu so=%d doff=%d", n, so, doff);
				CHECK(doff == 0 || dst[doff - 1] == 0, "underrun size=%zu doff=%d", n, doff);
				CHECK(dst[doff + n] == 0, "overrun size=%zu doff=%d", n, doff);
				runs++;
			}
		}
	}

	/* Store fault inside a chunk: the user buffer ends `avail` bytes before
	 * a PROT_NONE page; read() must report exactly the bytes it stored. */
	{
		size_t n = 65536, total = 0, ok = 0;
		unsigned char *map = mmap(NULL, 32 * PAGE, PROT_READ | PROT_WRITE,
					  MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		if (map == MAP_FAILED) { perror("mmap"); return 2; }
		mprotect(map + 31 * PAGE, PAGE, PROT_NONE);
		if (pwrite(fd, src, n, 0) != (ssize_t)n) { perror("pwrite"); return 2; }
		for (size_t avail = 16387; avail < 16387 + 4096 * 3; avail += 997) {
			unsigned char *p = map + 31 * PAGE - avail;	/* odd alignment */
			ssize_t r = pread(fd, p, n, 0);
			total++;
			/* copy_to_user may under-report (word-granular scalar fixup)
			 * but must never claim bytes it did not store. */
			if (r >= 0 && r <= (ssize_t)avail && avail - r < 64 && !memcmp(p, src, r))
				ok++;
			else
				printf("FAIL fault avail=%zu got=%zd\n", avail, r), fail++;
			if (r != (ssize_t)avail)
				printf("note fault avail=%zu got=%zd (short by %zd)\n", avail, r, (ssize_t)avail - r);
		}
		printf("fault cases ok=%zu/%zu\n", ok, total);
	}
	close(fd);
	unlink(path);
	printf("%s copies=%d failures=%d\n", fail ? "FAIL" : "PASS", runs, fail);
	return fail ? 1 : 0;
}
