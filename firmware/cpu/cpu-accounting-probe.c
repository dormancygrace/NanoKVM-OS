// Compare tick accounting with scheduler runtime and a monotonic wall clock.
#define _POSIX_C_SOURCE 200809L
#include <dirent.h>
#include <errno.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

struct sample {
    uint64_t wall_ns, cpu[10], task_ticks;
    double thread_runtime_ms;
    unsigned threads;
};
static uint64_t now_ns(void) {
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts)) { perror("clock_gettime"); exit(1); }
    return (uint64_t)ts.tv_sec * 1000000000ULL + ts.tv_nsec;
}
static void sample(int pid, struct sample *s) {
    char path[160], line[4096];
    memset(s, 0, sizeof(*s));
    s->wall_ns = now_ns();
    FILE *f = fopen("/proc/stat", "r");
    if (!f || !fgets(line, sizeof(line), f)) { perror("/proc/stat"); exit(1); }
    fclose(f);
    char *cur = line + 4;
    for (unsigned i = 0; i < 10; ++i) s->cpu[i] = strtoull(cur, &cur, 10);
    snprintf(path, sizeof(path), "/proc/%d/stat", pid);
    f = fopen(path, "r");
    if (!f || !fgets(line, sizeof(line), f)) { perror(path); exit(1); }
    fclose(f);
    cur = strrchr(line, ')');
    if (!cur) exit(1);
    cur += 2;
    char *save = NULL;
    unsigned field = 3;
    for (char *t = strtok_r(cur, " ", &save); t; t = strtok_r(NULL, " ", &save), ++field)
        if (field == 14 || field == 15) s->task_ticks += strtoull(t, NULL, 10);
    snprintf(path, sizeof(path), "/proc/%d/task", pid);
    DIR *d = opendir(path);
    if (!d) { perror(path); exit(1); }
    struct dirent *entry;
    while ((entry = readdir(d))) {
        if (entry->d_name[0] < '0' || entry->d_name[0] > '9') continue;
        snprintf(path, sizeof(path), "/proc/%d/task/%ld/sched", pid, strtol(entry->d_name, NULL, 10));
        f = fopen(path, "r");
        if (!f) continue; // A thread may exit between directory and file reads.
        while (fgets(line, sizeof(line), f)) {
            double ms;
            if (sscanf(line, "se.sum_exec_runtime : %lf", &ms) == 1) {
                s->thread_runtime_ms += ms;
                s->threads++;
                break;
            }
        }
        fclose(f);
    }
    closedir(d);
}
int main(int argc, char **argv) {
    if (argc != 3) { fprintf(stderr, "usage: %s PID SECONDS\n", argv[0]); return 2; }
    char *end;
    long pid = strtol(argv[1], &end, 10);
    if (*end || pid < 1 || pid > 4194304) return 2;
    long seconds = strtol(argv[2], &end, 10);
    if (*end || seconds < 1 || seconds > 60) return 2;
    long hz = sysconf(_SC_CLK_TCK), cpus = sysconf(_SC_NPROCESSORS_ONLN);
    if (hz <= 0 || cpus <= 0) return 1;
    struct sample a, b;
    sample((int)pid, &a);
    struct timespec delay = {.tv_sec = seconds};
    while (nanosleep(&delay, &delay)) {
        if (errno != EINTR) { perror("nanosleep"); return 1; }
    }
    sample((int)pid, &b);
    double wall = (b.wall_ns - a.wall_ns) / 1e9;
    uint64_t ticks = 0;
    // Guest fields are already included in user/nice; don't count twice.
    for (unsigned i = 0; i < 8; ++i) ticks += b.cpu[i] - a.cpu[i];
    uint64_t idle = b.cpu[3] + b.cpu[4] - a.cpu[3] - a.cpu[4];
    printf("{\"pid\":%ld,\"hz\":%ld,\"cpus\":%ld,\"wall_seconds\":%.6f,"
           "\"aggregate_tick_seconds\":%.6f,\"task_tick_seconds\":%.6f,"
           "\"thread_runtime_seconds\":%.6f,\"threads_before\":%u,\"threads_after\":%u,"
           "\"task_wall_percent\":%.3f,\"thread_runtime_wall_percent\":%.3f,"
           "\"task_aggregate_percent\":%.3f,\"total_busy_aggregate_percent\":%.3f}\n",
           pid,hz,cpus,wall,(double)ticks/hz,(double)(b.task_ticks-a.task_ticks)/hz,
           (b.thread_runtime_ms-a.thread_runtime_ms)/1000,a.threads,b.threads,
           100*(b.task_ticks-a.task_ticks)/hz/wall,
           (b.thread_runtime_ms-a.thread_runtime_ms)/10/wall,
           100.0*(b.task_ticks-a.task_ticks)/ticks,100.0*(ticks-idle)/ticks);
    return 0;
}
