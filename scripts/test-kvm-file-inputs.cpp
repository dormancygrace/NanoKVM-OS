// Included after the production get/set HDMI mode and ION-check functions.
#include <algorithm>
#include <limits>
#include <stdexcept>
#include <vector>

static unsigned checks = 0;
static void expect(bool condition, const char *description) {
    ++checks;
    if (!condition) throw std::runtime_error(description);
}

static void write_file(const std::string &path, const std::string &contents) {
    FILE *fp = std::fopen(path.c_str(), "w");
    expect(fp != nullptr, "open fixture");
    expect(std::fwrite(contents.data(), 1, contents.size(), fp) == contents.size(), "write fixture");
    expect(std::fclose(fp) == 0, "close fixture");
}

static ssize_t partial_read_error(void *cookie, char *buffer, size_t size) {
    unsigned &calls = *static_cast<unsigned *>(cookie);
    if (calls++ == 0) {
        const char text[] = "usage rate:100%, memory usage peak 123 bytes\n";
        const size_t count = std::min(size, sizeof(text) - 1);
        std::memcpy(buffer, text, count);
        return static_cast<ssize_t>(count);
    }
    errno = EIO;
    return -1;
}

static void test_ion(const std::string &path, const std::string &directory) {
    // Exercise all valid percentages, including the 94/95 threshold and 100.
    for (int rate = 0; rate <= 100; ++rate) {
        const std::string text = "Summary:\n[0] carveout heap size:78643200 bytes, used:31010816 bytes\n  usage rate:"
            + std::to_string(rate) + "%, memory usage peak 31010816 bytes\n";
        expect(nanokvm::ion_usage_rate_from_summary(text.c_str()) == rate, "parse percentage");
        write_file(path, text);
        expect(chack_ion() == (rate >= 95 ? 1 : 2), "watchdog threshold");
    }
    for (const char *text : {"", "Summary:\n", "usage rate:%", "usage rate:-1%", "usage rate:+95%",
        "usage rate:101%", "usage rate:99999999999999999999999999%", "usage rate:100",
        "usage rate:100junk%", "usage rate:95.5%", "usage rate:\n95%", "not usage rate:100%"}) {
        expect(nanokvm::ion_usage_rate_from_summary(text) == -1, "reject malformed percentage");
        write_file(path, text);
        expect(chack_ion() == 0, "malformed input cannot reboot");
    }
    expect(nanokvm::ion_usage_rate_from_summary(nullptr) == -1, "reject null summary");
    expect(nanokvm::ion_usage_rate_from_summary("\tusage rate:\t95 %") == 95, "allow horizontal whitespace");
    expect(nanokvm::ion_usage_rate_from_stream(nullptr) == -1, "reject null stream");
    expect(nanokvm::ion_usage_rate_from_file(nullptr) == -1, "reject null path");
    expect(unlink(path.c_str()) == 0, "remove ION fixture");
    expect(chack_ion() == 0, "missing debugfs input cannot reboot");
    expect(nanokvm::ion_usage_rate_from_file(directory.c_str()) == -1, "directory read error");

    unsigned calls = 0;
    cookie_io_functions_t functions = {};
    functions.read = partial_read_error;
    FILE *fp = fopencookie(&calls, "r", functions);
    expect(fp != nullptr, "open failing stream");
    expect(nanokvm::ion_usage_rate_from_stream(fp) == -1, "reject read error after valid percentage");
    expect(std::ferror(fp) != 0, "read error was exercised");
    expect(std::fclose(fp) == 0, "close failing stream");

    write_file(path, "usage rate:100%\n" + std::string(100000, 'x'));
    expect(chack_ion() == 1, "ignore long per-buffer table");
    write_file(path, std::string(511, ' ') + "usage rate:100%\n");
    expect(chack_ion() == 0, "bounded read does not scan entire table");
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

static void test_mode(const std::string &path, const std::string &directory) {
    write_file(path, "1\n");
    fake_now_ms = 100;
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 1, "first read reports new mode");
    expect(mode_open_count == 1 && hdmi_mode_stamp.valid, "record successful read");
    for (fake_now_ms = 101; fake_now_ms < 1100; fake_now_ms += 10) {
        expect(get_hdmi_mode() == 0 && kvmv_cfg.hdmi_mode == 1, "skip unchanged polling read");
    }
    expect(mode_open_count == 1, "unchanged polls never reopen mode file");
    fake_now_ms = 1100;
    expect(get_hdmi_mode() == 0 && mode_open_count == 2, "refresh unchanged mode at one second");

    struct stat original;
    expect(stat(path.c_str(), &original) == 0, "stat mode fixture");
    write_file(path, "2\n");
    const struct timespec times[] = {original.st_atim, original.st_mtim};
    expect(utimensat(AT_FDCWD, path.c_str(), times, 0) == 0, "restore same-size rewrite timestamp");
    fake_now_ms = 2099;
    expect(get_hdmi_mode() == 0 && kvmv_cfg.hdmi_mode == 1 && mode_open_count == 2,
        "same stamp remains cached before bound");
    fake_now_ms = 2100;
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 2 && mode_open_count == 3,
        "bounded refresh sees identical-stamp rewrite");

    const std::string replacement = path + ".new";
    write_file(replacement, "0\n");
    expect(utimensat(AT_FDCWD, replacement.c_str(), times, 0) == 0, "replacement timestamp");
    expect(rename(replacement.c_str(), path.c_str()) == 0, "replace mode inode");
    fake_now_ms = 2101;
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 0, "replacement inode is read immediately");

    expect(set_hdmi_mode(1) == 1 && !hdmi_mode_stamp.valid, "own durable write invalidates stamp");
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 1, "own write is seen next pass");
    expect(set_hdmi_mode(3) == 0 && hdmi_mode_stamp.valid, "invalid mode cannot change file");
    write_file(path, "9\n");
    fake_now_ms += 1000;
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 0 && !hdmi_mode_stamp.valid,
        "invalid file mode resets through durable writer");
    expect(get_hdmi_mode() == 0 && hdmi_mode_stamp.valid, "read corrected zero mode next pass");

    fail_mode_open = true;
    fake_now_ms += 1000;
    expect(get_hdmi_mode() == 0 && !hdmi_mode_stamp.valid && kvmv_cfg.hdmi_mode == 0,
        "open failure resets mode and invalidates cache");
    fail_mode_open = false;
    expect(get_hdmi_mode() == 0 && hdmi_mode_stamp.valid, "retry immediately after open failure");

    expect(unlink(path.c_str()) == 0, "remove mode fixture");
    kvmv_cfg.hdmi_mode = 2;
    expect(get_hdmi_mode() == 0 && !hdmi_mode_stamp.valid && kvmv_cfg.hdmi_mode == 0,
        "missing file preserves reset/return behavior");
    write_file(path, "2\n");
    expect(get_hdmi_mode() == 1 && kvmv_cfg.hdmi_mode == 2, "recreated file read immediately");

    hdmi_mode_path = directory.c_str();
    expect(get_hdmi_mode() == 0 && !hdmi_mode_stamp.valid && kvmv_cfg.hdmi_mode == 0,
        "read error resets mode and invalidates cache");
    hdmi_mode_stamp.valid = true;
    expect(set_hdmi_mode(1) == 0 && !hdmi_mode_stamp.valid, "failed own write invalidates stamp");
    hdmi_mode_path = path.c_str();
}

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    const std::string directory = argv[1];
    const std::string mode = directory + "/hdmi_mode";
    const std::string ion = directory + "/ion_summary";
    hdmi_mode_path = mode.c_str();
    ion_summary_path = ion.c_str();
    try {
        test_ion(ion, directory);
        test_stamp();
        test_mode(mode, directory);
    } catch (const std::exception &error) {
        std::fprintf(stderr, "FAIL: %s\n", error.what());
        return 1;
    }
    std::printf("Native ION and HDMI mode regressions passed (%u checks)\n", checks);
    return 0;
}
