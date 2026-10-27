// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

static void die(const char *what) { perror(what); exit(1); }
static uint64_t now(clockid_t clock)
{
	struct timespec t;
	if (clock_gettime(clock, &t)) die("clock");
	return (uint64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}
static void transfer(int fd[2], unsigned char *src, unsigned char *dst, unsigned n)
{
	ssize_t w = write(fd[1], src, n), got = 0;
	if (w != (ssize_t)n) die("short write");
	do {
		ssize_t r = read(fd[0], dst + got, n - got);
		if (r < 0 || (!r && n)) die("short read");
		got += r;
	} while (got < (ssize_t)n);
}
int main(int argc, char **argv)
{
#ifdef USERCOPY_LAYOUT
	const unsigned sizes[] = {2920, 3776};
#else
	const unsigned sizes[] = {0,1,2,3,4,7,8,9,15,16,17,31,32,33,
		63,64,65,71,72,127,128,129,255,256,257,768,
		1400,1460,2920,3776,4095,4096,4097,16384,32768};
#endif
	unsigned char *src = mmap(NULL, 65536, PROT_READ | PROT_WRITE,
		MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	unsigned char *dst = mmap(NULL, 65536, PROT_READ | PROT_WRITE,
		MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	int fd[2], socket_mode;
	if (argc != 2 || (strcmp(argv[1], "pipe") && strcmp(argv[1], "socket"))) {
		fprintf(stderr, "usage: %s pipe|socket\n", argv[0]); return 2;
	}
	socket_mode = !strcmp(argv[1], "socket");
	if (src == MAP_FAILED || dst == MAP_FAILED) die("mmap");
	if (socket_mode) {
		int capacity = 131072;
		if (socketpair(AF_UNIX, SOCK_STREAM | SOCK_CLOEXEC, 0, fd)) die("socketpair");
		if (setsockopt(fd[1], SOL_SOCKET, SO_SNDBUF, &capacity, sizeof(capacity))) die("send capacity");
	} else {
		if (pipe2(fd, O_CLOEXEC)) die("pipe");
		if (fcntl(fd[1], F_SETPIPE_SZ, 65536) < 65536) die("pipe capacity");
	}
	for (unsigned i = 0; i < 65536; i++) src[i] = (i * 37 + (i >> 8)) & 255;
#ifdef USERCOPY_LAYOUT
	puts("mode,n,offset_index,rep,calls,wall_ns,cpu_ns");
#else
	puts("mode,n,alignment,rep,calls,wall_ns,cpu_ns");
#endif
	for (unsigned rep = 0; rep < 3; rep++) {
#ifdef USERCOPY_LAYOUT
		unsigned count = 8 * sizeof(sizes) / sizeof(sizes[0]);
#else
		unsigned count = 2 * sizeof(sizes) / sizeof(sizes[0]);
#endif
		for (unsigned j = 0; j < count; j++) {
			unsigned id = (j + rep * 23) % count;
#ifdef USERCOPY_LAYOUT
			/* Exercise eight aligned buffer placements in the two
			 * socket sizes with the largest observed allocation noise. */
			unsigned n = sizes[id / 8], a = id % 8, calls = 4096;
			unsigned char *s = src + 16 + 64 * a, *d = dst + 16 + 64 * a;
#else
			unsigned n = sizes[id / 2], a = id % 2;
			unsigned calls = n ? 16777216 / n : 32768;
			unsigned char *s = src + 16 + a, *d = dst + 16 + 3 * a;
#endif
			uint64_t wall, cpu;
			if (calls > 32768) calls = 32768;
			if (calls < 1024) calls = 1024;
			memset(dst, 0xa5, 65536);
			transfer(fd, s, d, n);
			wall = now(CLOCK_MONOTONIC_RAW); cpu = now(CLOCK_PROCESS_CPUTIME_ID);
			for (unsigned i = 0; i < calls; i++) transfer(fd, s, d, n);
			cpu = now(CLOCK_PROCESS_CPUTIME_ID) - cpu;
			wall = now(CLOCK_MONOTONIC_RAW) - wall;
			if (memcmp(s, d, n) || d[-1] != 0xa5 || d[n] != 0xa5) {
				fprintf(stderr, "payload/guard mismatch n=%u align=%u\n", n, a); return 1;
			}
			printf("%s,%u,%u,%u,%u,%llu,%llu\n", argv[1], n, a, rep, calls,
				(unsigned long long)wall, (unsigned long long)cpu);
			fflush(stdout);
		}
	}
	close(fd[0]); close(fd[1]); munmap(src, 65536); munmap(dst, 65536);
	return 0;
}
