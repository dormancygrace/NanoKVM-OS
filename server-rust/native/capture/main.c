#include "nk-capture-protocol.h"
#include <string.h>
int main(int argc, char **argv) {
    if (argc != 3 || strcmp(argv[1], "--fd") || strcmp(argv[2], "3")) return 2;
    return nk_capture_run(3) == 0 ? 0 : 1;
}
