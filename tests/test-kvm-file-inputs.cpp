// Included after the production get/set HDMI mode and rejected-input functions.
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

static std::string read_fd(int fd) {
    char text[64] = {};
    const ssize_t count = read(fd, text, sizeof(text));
    expect(count >= 0, "read published record");
    return std::string(text, static_cast<size_t>(count));
}

static std::string read_file(const std::string &path) {
    int fd = open(path.c_str(), O_RDONLY);
    expect(fd >= 0, "open published record");
    const std::string text = read_fd(fd);
    close(fd);
    return text;
}

static void test_rejected_input(const std::string &directory) {
    const std::string path = directory + "/rejected_input";
    const std::string tmp = path + ".tmp";
    rejected_input_path = path.c_str();
    rejected_input_tmp_path = tmp.c_str();
    publish_rejected_input(0, 0); // Missing record is already clear.
    publish_rejected_input(2560, 1440);
    expect(read_file(path) == "2560 1440\n", "publish complete pair");
    int previous = open(path.c_str(), O_RDONLY);
    expect(previous >= 0, "open previous generation");
    publish_rejected_input(2160, 3840);
    expect(read_fd(previous) == "2560 1440\n", "open reader retains complete old pair");
    close(previous);
    expect(read_file(path) == "2160 3840\n", "new reader sees complete replacement");
    struct stat before, after;
    expect(stat(path.c_str(), &before) == 0, "stat published record");
    publish_rejected_input(2160, 3840);
    expect(stat(path.c_str(), &after) == 0 && before.st_ino == after.st_ino,
        "unchanged publication avoids filesystem writes");

    expect(mkdir(tmp.c_str(), 0700) == 0, "block temporary write");
    publish_rejected_input(3840, 2160);
    expect(read_file(path) == "2160 3840\n", "write failure preserves complete old record");
    expect(rmdir(tmp.c_str()) == 0, "unblock temporary write");
    publish_rejected_input(3840, 2160);
    expect(read_file(path) == "3840 2160\n", "same size retries after write failure");

    expect(unlink(path.c_str()) == 0 && mkdir(path.c_str(), 0700) == 0, "block rename");
    publish_rejected_input(1440, 2560);
    expect(access(tmp.c_str(), F_OK) != 0, "failed rename removes temporary record");
    expect(rmdir(path.c_str()) == 0, "unblock rename");
    publish_rejected_input(1440, 2560);
    expect(read_file(path) == "1440 2560\n", "same size retries after rename failure");

    expect(unlink(path.c_str()) == 0 && mkdir(path.c_str(), 0700) == 0, "block clear");
    publish_rejected_input(0, 0);
    expect(rmdir(path.c_str()) == 0, "unblock clear");
    write_file(path, "1440 2560\n");
    publish_rejected_input(0, 0);
    expect(access(path.c_str(), F_OK) != 0, "failed clear retries without caching success");
}

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    const std::string directory = argv[1];
    const std::string mode = directory + "/hdmi_mode";
    hdmi_mode_path = mode.c_str();
    try {
        test_stamp();
        test_mode(mode, directory);
        test_rejected_input(directory);
    } catch (const std::exception &error) {
        std::fprintf(stderr, "FAIL: %s\n", error.what());
        return 1;
    }
    std::printf("Native HDMI mode and rejected-input regressions passed (%u checks)\n", checks);
    return 0;
}
