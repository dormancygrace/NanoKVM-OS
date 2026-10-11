/* Hardware-free loader/API-presence probe. Supply the expected ELF symbols.
 * Compile this runner for rv64gc when the host emulator lacks C906 vector 0.7.
 * It does not exercise frame processing or validate hardware behavior.
 */
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

int main(int argc, char **argv)
{
    if (argc < 3) {
        fprintf(stderr, "usage: %s library symbol...\n", argv[0]);
        return 2;
    }
    void *handle = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!handle) {
        fprintf(stderr, "dlopen: %s\n", dlerror());
        return 1;
    }
    for (int i = 2; i < argc; ++i) {
        dlerror();
        void *symbol = dlsym(handle, argv[i]);
        const char *error = dlerror();
        if (!symbol || error) {
            fprintf(stderr, "missing %s: %s\n", argv[i], error ? error : "NULL");
            return 1;
        }
    }
    if (dlclose(handle) != 0)
        return 1;
    printf("PASS: RTLD_NOW and %d API symbols; no hardware calls\n", argc - 2);
    return 0;
}
