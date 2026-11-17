// paths OP N DIR: N passes of one kernel path, for its function profile (profile.sh).
// OP: pipe (round trip between two processes), stat, open (open+close), getppid, fault (touch fresh anonymous
// pages, 128 per mapping), read / write (64 KiB pread / pwrite of a tmpfs file). Files: DIR/aaa/bbb/ccc/ddd.
#define _GNU_SOURCE
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

int main(int argc, char **argv)
{
	if (argc != 4)
		return 2;
	const char *op = argv[1];
	int n = atoi(argv[2]);
	char path[256], cmd[600];
	static char buf[65536];
	struct stat st;

	snprintf(path, sizeof path, "%s/aaa/bbb/ccc/ddd", argv[3]);
	snprintf(cmd, sizeof cmd, "mkdir -p %s/aaa/bbb/ccc && touch %s", argv[3], path);
	if (system(cmd))
		return 1;
	if (!strcmp(op, "pipe")) {
		int a[2], b[2];
		char c = 0;
		if (pipe(a) || pipe(b))
			return 1;
		pid_t p = fork();
		if (p < 0)
			return 1;
		if (!p) {
			close(a[1]);
			close(b[0]);
			for (int i = 0; i < n; i++)
				if (read(a[0], &c, 1) != 1 || write(b[1], &c, 1) != 1)
					_exit(1);
			_exit(0);
		}
		close(a[0]);
		close(b[1]);
		for (int i = 0; i < n; i++)
			if (write(a[1], &c, 1) != 1 || read(b[0], &c, 1) != 1)
				return 1;
		int status;
		if (waitpid(p, &status, 0) != p || !WIFEXITED(status) || WEXITSTATUS(status))
			return 1;
	} else if (!strcmp(op, "stat")) {
		for (int i = 0; i < n; i++)
			if (stat(path, &st))
				return 1;
	} else if (!strcmp(op, "open")) {
		for (int i = 0; i < n; i++)
			if (close(open(path, O_RDONLY)))
				return 1;
	} else if (!strcmp(op, "getppid")) {
		for (int i = 0; i < n; i++)
			getppid();
	} else if (!strcmp(op, "fault")) {
		for (int i = 0; i < n; i += 128) {
			char *m = mmap(0, 128 << 12, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
			if (m == MAP_FAILED)
				return 1;
			for (int o = 0; o < 128 << 12; o += 4096)
				m[o] = 1;
			munmap(m, 128 << 12);
		}
	} else if (!strcmp(op, "read") || !strcmp(op, "write")) {
		int fd = open(path, O_RDWR), wr = op[0] == 'w';
		if (fd < 0 || pwrite(fd, buf, sizeof buf, 0) != sizeof buf)
			return 1;
		for (int i = 0; i < n; i++)
			if ((wr ? pwrite(fd, buf, sizeof buf, 0) : pread(fd, buf, sizeof buf, 0)) != sizeof buf)
				return 1;
	} else {
		return 2;
	}
	return 0;
}
