#define _GNU_SOURCE
#include <sched.h>
#include <sys/mount.h>
#include <sys/wait.h>
#include <sys/prctl.h>
#include <unistd.h>
#include <stdio.h>
#include <stdlib.h>
#include <signal.h>
#include <errno.h>
#include <dirent.h>
#include <string.h>

static pid_t child = -1;
static void deadline(int sig) { (void)sig; if (child > 0) kill(child, SIGKILL); }
static void cleanup(void) {
    // The old kernel lacks PID namespaces. Adopt and terminate only our own
    // descendants, including udhcpd after its daemonizing parent exits.
    for (;;) {
        DIR *d = opendir("/proc");
        if (!d) { perror("proc"); exit(1); }
        struct dirent *entry;
        while ((entry = readdir(d))) {
            char *end;
            long pid = strtol(entry->d_name, &end, 10);
            if (*end || pid <= 1) continue;
            char path[100], stat[1024];
            snprintf(path, sizeof(path), "/proc/%ld/stat", pid);
            FILE *f = fopen(path, "r");
            if (!f) continue;
            if (fgets(stat, sizeof(stat), f)) {
                char *comm_end = strrchr(stat, ')'), state;
                long parent;
                if (comm_end && sscanf(comm_end + 1, " %c %ld", &state, &parent) == 2 && parent == getpid())
                    kill((pid_t)pid, SIGKILL);
            }
            fclose(f);
        }
        closedir(d);
        int status;
        if (waitpid(-1, &status, 0) < 0) {
            if (errno == EINTR) continue;
            if (errno != ECHILD) perror("reap");
            break;
        }
    }
}
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    if (prctl(PR_SET_CHILD_SUBREAPER, 1)) { perror("subreaper"); return 1; }
    if (unshare(CLONE_NEWNET | CLONE_NEWNS)) { perror("unshare"); return 1; }
    if (mount(NULL, "/", NULL, MS_REC | MS_PRIVATE, NULL)) { perror("mount private"); return 1; }
    child = fork();
    if (child < 0) { perror("fork"); return 1; }
    if (child == 0) {
        if (chroot(argv[1]) || chdir("/")) { perror("chroot"); _exit(1); }
        if (mount("proc", "/proc", "proc", MS_NOSUID | MS_NODEV | MS_NOEXEC, NULL)) { perror("proc"); _exit(1); }
        execl("/bin/sh", "sh", argv[2], (char *)NULL);
        perror("exec"); _exit(1);
    }
    signal(SIGALRM, deadline);
    alarm(45);
    int status;
    while (waitpid(child, &status, 0) < 0) { if (errno == EINTR) continue; perror("wait"); return 1; }
    alarm(0);
    cleanup();
    return WIFEXITED(status) ? WEXITSTATUS(status) : 128 + WTERMSIG(status);
}
