#include "nanokvm_capture_size.h"
#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>

static int read_dimension(const char *directory, const char *name, uint32_t *value)
{
    char path[PATH_MAX], text[32];
    int length = snprintf(path, sizeof(path), "%s/%s", directory, name);
    if (length < 0 || (size_t)length >= sizeof(path)) return -ENAMETOOLONG;
    FILE *file = fopen(path, "r");
    if (!file) return -errno;
    if (!fgets(text, sizeof(text), file)) {
        fclose(file);
        return -EINVAL;
    }
    int extra = fgetc(file), failed = ferror(file);
    fclose(file);
    if (extra != EOF || failed) return -EINVAL;
    char *start = text, *end;
    while (isspace((unsigned char)*start)) start++;
    if (!isdigit((unsigned char)*start)) return -EINVAL;
    errno = 0;
    unsigned long parsed = strtoul(start, &end, 10);
    if (errno || !parsed || parsed > UINT16_MAX) return -ERANGE;
    while (isspace((unsigned char)*end)) end++;
    if (*end) return -EINVAL;
    *value = (uint32_t)parsed;
    return 0;
}

int nanokvm_read_capture_size(const char *directory, uint32_t *width, uint32_t *height)
{
    if (!directory || !width || !height) return -EINVAL;
    uint32_t w, h;
    int wr = read_dimension(directory, "width", &w);
    int hr = read_dimension(directory, "height", &h);
    if (wr == -ENOENT && hr == -ENOENT) {
        w = 1920; h = 1080;
    } else if (wr || hr) {
        return wr ? wr : hr;
    }
    *width = w; *height = h;
    return 0;
}
