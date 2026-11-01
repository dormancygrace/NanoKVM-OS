// Pure FileStamp cache logic: metadata change detection and deadline across clock wrap.
#include "internal/file_stamp.hpp"
#include <cstdint>
#include <cstdio>
#include <limits>
#include <stdexcept>

static unsigned checks = 0;
static void expect(bool condition, const char *description) {
    ++checks;
    if (!condition) throw std::runtime_error(description);
}

static void test_stamp() {
    struct stat st = {};
    st.st_dev = 1;
    st.st_ino = 2;
    st.st_size = 3;
    st.st_mtim.tv_sec = 4;
    st.st_mtim.tv_nsec = 5;
    nanokvm::FileStamp stamp;
    expect(nanokvm::file_stamp_due(stamp, st, 50, 1000), "initial stamp is due");
    nanokvm::file_stamp_record(stamp, st, 50);
    expect(!nanokvm::file_stamp_due(stamp, st, 1049, 1000), "skip unchanged stamp before deadline");
    expect(nanokvm::file_stamp_due(stamp, st, 1050, 1000), "refresh at deadline");
    for (int field = 0; field < 5; ++field) {
        struct stat changed = st;
        switch (field) {
            case 0: ++changed.st_dev; break;
            case 1: ++changed.st_ino; break;
            case 2: ++changed.st_size; break;
            case 3: ++changed.st_mtim.tv_sec; break;
            case 4: ++changed.st_mtim.tv_nsec; break;
        }
        expect(nanokvm::file_stamp_due(stamp, changed, 51, 1000), "each metadata change invalidates cache");
    }
    nanokvm::file_stamp_record(stamp, st, std::numeric_limits<uint32_t>::max() - 499);
    expect(!nanokvm::file_stamp_due(stamp, st, 499, 1000), "cache age before deadline across clock wrap");
    expect(nanokvm::file_stamp_due(stamp, st, 500, 1000), "cache expires across clock wrap");
    stamp.valid = false;
    expect(nanokvm::file_stamp_due(stamp, st, 0, 1000), "explicit invalidation is due");
}

int main() {
    try {
        test_stamp();
    } catch (const std::exception &error) {
        std::fprintf(stderr, "FAIL: %s\n", error.what());
        return 1;
    }
    std::printf("Native FileStamp regressions passed (%u checks)\n", checks);
    return 0;
}
