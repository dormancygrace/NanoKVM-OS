// Minimal unshare -n shim for test devices without util-linux unshare.
// A successful unshare is mandatory before executing any namespace setup.
#define _GNU_SOURCE
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
int main(int argc, char **argv) {
 if (argc < 3 || strcmp(argv[1], "-n") != 0) {
  fputs("usage: pmtu-netns-launch -n program [arguments]\n", stderr);
  return 2;
 }
 if (unshare(CLONE_NEWNET) != 0) { perror("unshare network"); return 1; }
 execvp(argv[2], argv + 2);
 perror("exec namespace program");
 return 1;
}
