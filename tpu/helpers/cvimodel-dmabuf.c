// cvimodel-dmabuf IN OUT: a copy of a cv181x model whose command buffers are
// stored as DMA buffers, the form the runtime turns them into when it loads
// a model. Converting them at load took 0.29 s of the text detector's 0.45 s
// on the NanoKVM (11.6 MB of commands); the runtime takes a DMABUF section
// as it is and only puts the addresses in. Built for the build machine with
// the runtime's own converter (cviruntime/src/soc/181x/bm_dmabuf.c).
//
// The model's flatbuffer is changed in place: the type and the size of each
// command buffer section and the offsets of the sections after it. A field
// the model does not store (it holds the default) cannot be changed so; the
// model is then refused and stays as it is.
//
// The converter stamps the time of the conversion and an MD5 of the model,
// which the runtime does not check (model.cpp: "TODO, verify md5"). Both are
// fixed here, the time to the epoch and the digest to zeros, so the same
// conversion gives the same file and a published model can be compared with
// the one checked on a device.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

int cvi181x_dmabuf_size(uint8_t *cmdbuf, size_t sz, size_t *psize, size_t *pmu_size);
int cvi181x_dmabuf_convert(uint8_t *cmdbuf, size_t sz, uint8_t *dmabuf);

enum { HEADER_SIZE = 48, SECTION_CMDBUF = 1, SECTION_DMABUF = 4 };
// The header's MD5 (runtime/model.hpp MODEL_HEADER).
enum { HEADER_MD5 = 14, MD5_SIZE = 16 };
// Field indexes in cvibuilder/proto/cvimodel.fbs.
enum { MODEL_BUILD_TIME = 2, MODEL_SECTIONS = 7 };
static const char build_time[] = "1970-01-01 00:00:00";
enum { SECTION_TYPE, SECTION_NAME, SECTION_SIZE, SECTION_OFFSET, SECTION_ENCRYPT, SECTION_COMPRESS };

static uint8_t *body;
static size_t body_size;

static uint32_t u32(size_t at) { uint32_t v; memcpy(&v, body + at, 4); return v; }
static void set_u32(size_t at, uint32_t v) { memcpy(body + at, &v, 4); }

// Where field `field` of the table at `table` is, or 0 when it is not stored.
static size_t field(size_t table, int field) {
    int32_t back;
    memcpy(&back, body + table, 4);
    const size_t vtable = table - back;
    uint16_t vt_size, at = 0;
    memcpy(&vt_size, body + vtable, 2);
    if (4 + 2 * field + 2 <= vt_size) memcpy(&at, body + vtable + 4 + 2 * field, 2);
    return at ? table + at : 0;
}

static void fail(const char *what) {
    fprintf(stderr, "cvimodel-dmabuf: %s\n", what);
    exit(1);
}

int main(int argc, char **argv) {
    if (argc != 3) fail("usage: cvimodel-dmabuf IN.cvimodel OUT.cvimodel");
    FILE *in = fopen(argv[1], "rb");
    if (!in) fail("cannot open the model");
    fseek(in, 0, SEEK_END);
    const size_t length = ftell(in);
    fseek(in, 0, SEEK_SET);
    uint8_t *file = malloc(length);
    if (!file || fread(file, 1, length, in) != length) fail("cannot read the model");
    fclose(in);
    if (length < HEADER_SIZE || memcmp(file, "CviModel", 8) || (file[12] == 1 && file[13] == 0))
        fail("not a cvimodel of version 1.1 or later");
    uint32_t stored_size;
    memcpy(&stored_size, file + 8, 4);
    body_size = stored_size;
    if (HEADER_SIZE + body_size > length) fail("short model");
    body = file + HEADER_SIZE;
    const uint8_t *binary = body + body_size;
    const size_t binary_size = length - HEADER_SIZE - body_size;

    const size_t model = u32(0);
    const size_t sections_at = field(model, MODEL_SECTIONS);
    if (!sections_at) fail("no sections");
    const size_t sections = sections_at + u32(sections_at);
    const uint32_t count = u32(sections);

    uint8_t *out = NULL;
    size_t out_size = 0, converted = 0;
    for (uint32_t i = 0; i < count; ++i) {
        const size_t entry = sections + 4 + 4 * i;
        const size_t section = entry + u32(entry);
        const size_t type_at = field(section, SECTION_TYPE), size_at = field(section, SECTION_SIZE);
        const size_t offset_at = field(section, SECTION_OFFSET);
        const size_t encrypt_at = field(section, SECTION_ENCRYPT), compress_at = field(section, SECTION_COMPRESS);
        const uint8_t type = type_at ? body[type_at] : 0;
        const uint32_t size = size_at ? u32(size_at) : 0, offset = offset_at ? u32(offset_at) : 0;
        if ((size_t)offset + size > binary_size) fail("a section is past the end of the model");
        const uint8_t *data = binary + offset;
        uint8_t *dmabuf = NULL;
        size_t new_size = size;
        if (type == SECTION_CMDBUF && size) {
            if ((encrypt_at && body[encrypt_at]) || (compress_at && body[compress_at]))
                fail("encrypted or compressed command buffers are not converted");
            size_t pmu_size = 0;
            uint8_t *cmdbuf = malloc(size);
            if (!cmdbuf) fail("out of memory");
            memcpy(cmdbuf, data, size);
            cvi181x_dmabuf_size(cmdbuf, size, &new_size, &pmu_size);
            // Zeroed, as the runtime's buffers come from the heap.
            dmabuf = calloc(1, new_size);
            if (!dmabuf || cvi181x_dmabuf_convert(cmdbuf, size, dmabuf) != 0) fail("conversion failed");
            free(cmdbuf);
            if (!type_at || !size_at) fail("the section's type or size is not stored");
            body[type_at] = SECTION_DMABUF;
            set_u32(size_at, new_size);
            data = dmabuf;
            ++converted;
        }
        if (out_size != offset) {
            if (!offset_at) fail("a section's offset is not stored");
            set_u32(offset_at, out_size);
        }
        out = realloc(out, out_size + new_size);
        if (!out && new_size) fail("out of memory");
        memcpy(out + out_size, data, new_size);
        out_size += new_size;
        free(dmabuf);
    }
    if (!converted) fail("no command buffer to convert");

    const size_t time_at = field(model, MODEL_BUILD_TIME);
    if (time_at) {
        const size_t time = time_at + u32(time_at);
        if (u32(time) != sizeof(build_time) - 1) fail("the build time is not YYYY-MM-DD HH:MM:SS");
        memcpy(body + time + 4, build_time, sizeof(build_time) - 1);
    }
    memset(file + HEADER_MD5, 0, MD5_SIZE);

    FILE *o = fopen(argv[2], "wb");
    if (!o || fwrite(file, 1, HEADER_SIZE + body_size, o) != HEADER_SIZE + body_size
        || fwrite(out, 1, out_size, o) != out_size || fclose(o) != 0)
        fail("cannot write the model");
    printf("%s: %zu command buffer(s) as DMA buffers, %zu -> %zu bytes\n", argv[2], converted, binary_size, out_size);
    return 0;
}
