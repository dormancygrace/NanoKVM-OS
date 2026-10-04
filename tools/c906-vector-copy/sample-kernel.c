// SPDX-License-Identifier: GPL-2.0-only
#define _GNU_SOURCE
#include <errno.h>
#include <inttypes.h>
#include <linux/perf_event.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <sys/syscall.h>
#include <time.h>
#include <unistd.h>

struct reader {
	int fd;
	struct perf_event_mmap_page *meta;
	unsigned char *data;
	size_t size;
	uint64_t tail;
};
static uint64_t ips[1000000], used, lost;

static uint64_t now_ns(void)
{
	struct timespec t;
	if (clock_gettime(CLOCK_MONOTONIC, &t)) {
		perror("clock_gettime");
		exit(1);
	}
	return (uint64_t)t.tv_sec * 1000000000 + t.tv_nsec;
}

static void ring_copy(struct reader *r, uint64_t pos, void *dst, size_t n)
{
	size_t off = pos & (r->size - 1), first = r->size - off;
	if (first > n)
		first = n;
	memcpy(dst, r->data + off, first);
	memcpy((unsigned char *)dst + first, r->data, n - first);
}

static int drain(struct reader *r)
{
	uint64_t head = __atomic_load_n(&r->meta->data_head, __ATOMIC_ACQUIRE);
	if (head - r->tail > r->size) {
		fprintf(stderr, "perf ring overrun\n");
		return -1;
	}
	while (r->tail < head) {
		struct perf_event_header h;
		ring_copy(r, r->tail, &h, sizeof(h));
		if (h.size < sizeof(h) || h.size > head - r->tail) {
			fprintf(stderr, "invalid perf record\n");
			return -1;
		}
		if (h.type == PERF_RECORD_SAMPLE) {
			uint64_t ip;
			if (h.size != sizeof(h) + sizeof(ip) || used == 1000000)
				return -1;
			ring_copy(r, r->tail + sizeof(h), &ip, sizeof(ip));
			ips[used++] = ip;
		} else if (h.type == PERF_RECORD_LOST) {
			uint64_t count;
			if (h.size < sizeof(h) + 16)
				return -1;
			ring_copy(r, r->tail + sizeof(h) + 8, &count, 8);
			lost += count;
		}
		r->tail += h.size;
	}
	__atomic_store_n(&r->meta->data_tail, r->tail, __ATOMIC_RELEASE);
	return 0;
}

static int cmp_ip(const void *a, const void *b)
{
	uint64_t x = *(const uint64_t *)a, y = *(const uint64_t *)b;
	return (x > y) - (x < y);
}

int main(int argc, char **argv)
{
	char *end;
	long seconds;
	if (argc != 2 || (seconds = strtol(argv[1], &end, 10)) < 1 ||
	    seconds > 180 || *end) {
		fprintf(stderr, "usage: %s SECONDS (1..180); root/kernel perf permission required\n", argv[0]);
		return 2;
	}
	long cpus = sysconf(_SC_NPROCESSORS_CONF), page = sysconf(_SC_PAGESIZE);
	if (cpus < 1 || cpus > 256 || page < 1)
		return 1;
	struct reader *readers = calloc((size_t)cpus, sizeof(*readers));
	if (!readers)
		return 1;
	int count = 0, failed = 0;
	for (int cpu = 0; cpu < cpus; cpu++) {
		struct perf_event_attr attr = {
			.type = PERF_TYPE_SOFTWARE,
			.size = sizeof(attr),
			.config = PERF_COUNT_SW_CPU_CLOCK,
			.sample_type = PERF_SAMPLE_IP,
			.sample_freq = 199,
			.freq = 1,
			.disabled = 1,
			.exclude_user = 1,
			.exclude_hv = 1,
			.wakeup_events = 1,
		};
		int fd = syscall(SYS_perf_event_open, &attr, -1, cpu, -1, PERF_FLAG_FD_CLOEXEC);
		if (fd < 0) {
			if (errno == ENODEV)
				continue; /* offline CPU */
			perror("perf_event_open");
			failed = 1;
			break;
		}
		size_t bytes = (size_t)page * 65;
		void *map = mmap(NULL, bytes, PROT_READ | PROT_WRITE, MAP_SHARED, fd, 0);
		if (map == MAP_FAILED) {
			perror("mmap");
			close(fd);
			failed = 1;
			break;
		}
		readers[count++] = (struct reader) {
			.fd = fd, .meta = map,
			.data = (unsigned char *)map + page, .size = (size_t)page * 64,
		};
		if (ioctl(fd, PERF_EVENT_IOC_ENABLE, 0)) {
			perror("PERF_EVENT_IOC_ENABLE");
			failed = 1;
			break;
		}
	}
	if (!count)
		failed = 1;
	uint64_t start = now_ns(), deadline = start + (uint64_t)seconds * 1000000000;
	while (!failed && now_ns() < deadline) {
		for (int i = 0; i < count; i++)
			if (drain(&readers[i]))
				failed = 1;
		struct timespec pause = {.tv_nsec = 20000000};
		while (nanosleep(&pause, &pause) && errno == EINTR) {}
	}
	for (int i = 0; i < count; i++) {
		if (ioctl(readers[i].fd, PERF_EVENT_IOC_DISABLE, 0))
			failed = 1;
		if (drain(&readers[i]))
			failed = 1;
		munmap(readers[i].meta, (size_t)page * 65);
		close(readers[i].fd);
	}
	free(readers);
	fprintf(stderr, "seconds=%.3f cpus=%d samples=%" PRIu64 " lost=%" PRIu64 "\n",
		(now_ns() - start) / 1e9, count, used, lost);
	if (failed || lost || !used)
		return 1;
	qsort(ips, (size_t)used, sizeof(ips[0]), cmp_ip);
	puts("ip,count");
	for (uint64_t i = 0; i < used;) {
		uint64_t next = i + 1;
		while (next < used && ips[next] == ips[i])
			next++;
		printf("%016" PRIx64 ",%" PRIu64 "\n", ips[i], next - i);
		i = next;
	}
	return 0;
}
