// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

static void die(const char *what) { perror(what); exit(1); }
static uint64_t ns(clockid_t id)
{
	struct timespec t;
	if (clock_gettime(id, &t)) die("clock");
	return (uint64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}
static void transfer(int fd[2], unsigned char *src, unsigned char *dst, unsigned n)
{
	if (write(fd[1], src, n) != (ssize_t)n || read(fd[0], dst, n) != (ssize_t)n) {
		errno = EIO;
		die("pipe transfer");
	}
}
int main(void)
{
	const unsigned sizes[] = {16, 64, 128, 1400, 4096, 16384, 32768};
	unsigned char *s = mmap(NULL, 65536, PROT_READ | PROT_WRITE,
		MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	unsigned char *d = mmap(NULL, 65536, PROT_READ | PROT_WRITE,
		MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	int fd[2];
	if (s == MAP_FAILED || d == MAP_FAILED || pipe2(fd, O_CLOEXEC)) die("setup");
	if (fcntl(fd[1], F_SETPIPE_SZ, 65536) < 65536) die("pipe capacity");
	for (unsigned i = 0; i < 65536; i++) s[i] = (i * 37 + (i >> 8)) & 255;
	puts("n,alignment,rep,calls,bytes,wall_ns,cpu_ns");
	for (unsigned rep = 0; rep < 9; rep++) {
		for (unsigned j = 0; j < 14; j++) {
			unsigned id = (j + rep * 5) % 14, n = sizes[id / 2], a = id % 2;
			unsigned calls = 67108864 / n;
			unsigned char *src = s + 16 + a, *dst = d + 16 + 3 * a;
			uint64_t wall, cpu;
			if (calls > 65536) calls = 65536;
			if (calls < 2048) calls = 2048;
			memset(d, 0xa5, 65536);
			transfer(fd, src, dst, n);
			wall = ns(CLOCK_MONOTONIC_RAW); cpu = ns(CLOCK_PROCESS_CPUTIME_ID);
			for (unsigned i = 0; i < calls; i++) transfer(fd, src, dst, n);
			cpu = ns(CLOCK_PROCESS_CPUTIME_ID) - cpu; wall = ns(CLOCK_MONOTONIC_RAW) - wall;
			if (memcmp(src, dst, n) || dst[-1] != 0xa5 || dst[n] != 0xa5) {
				fprintf(stderr, "payload/guard mismatch n=%u alignment=%u\n", n, a);
				return 1;
			}
			printf("%u,%u,%u,%u,%llu,%llu,%llu\n", n, a, rep, calls,
				(unsigned long long)n * calls, (unsigned long long)wall,
				(unsigned long long)cpu);
		}
	}
	close(fd[0]); close(fd[1]); munmap(s, 65536); munmap(d, 65536);
	return 0;
}
