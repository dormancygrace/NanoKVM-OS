// Exercise the actual MaixCDK system and filesystem implementations.
#include "maix_sys.hpp"
#include <cassert>
#include <cerrno>
#include <cstdio>
#include <cstring>
#include <map>
#include <set>
#include <string>

struct Fixture {
    std::string text;
    bool missing = false;
    bool read_error = false;
};
static std::map<std::string, Fixture> fixtures;
static std::set<FILE *> streams;
static unsigned opened, closed;
extern "C" FILE *__real_fopen(const char *, const char *);
extern "C" int __real_fclose(FILE *);
static ssize_t fail_read(void *, char *, size_t) {
    errno = EIO;
    return -1;
}
extern "C" FILE *__wrap_fopen(const char *path, const char *mode) {
    auto it = fixtures.find(path);
    if (it == fixtures.end())
        return __real_fopen(path, mode);
    if (it->second.missing) {
        errno = ENOENT;
        return nullptr;
    }
    FILE *file;
    if (it->second.read_error) {
        cookie_io_functions_t io = {};
        io.read = fail_read;
        file = fopencookie(nullptr, "r", io);
    } else {
        file = tmpfile();
        assert(file);
        assert(fwrite(it->second.text.data(), 1, it->second.text.size(), file)
               == it->second.text.size());
        rewind(file);
    }
    assert(file);
    streams.insert(file);
    ++opened;
    return file;
}
extern "C" int __wrap_fclose(FILE *file) {
    if (streams.erase(file))
        ++closed;
    return __real_fclose(file);
}
static void balanced() {
    assert(streams.empty());
    assert(opened == closed);
}
int main() {
    const char *clock = "/sys/kernel/debug/clk/clk_summary";
    const char *runtime = "/maixapp/maixcam_lib.version";
    fixtures["/boot/ver"] = {};
    fixtures["/device_key"] = {};
    fixtures[runtime] = {};
    fixtures[clock] = {};
    for (int i = 0; i != 256; ++i) {
        assert(maix::sys::os_version() == "Unkonwn");
        assert(maix::sys::device_key().empty());
        assert(maix::sys::runtime_version().empty());
        assert(maix::sys::cpu_freq().empty());
        assert(maix::sys::npu_freq().empty());
        balanced();
    }
    fixtures["/boot/ver"].text = "  NanoKVM OS\n";
    fixtures["/device_key"].text = "  fixture-key\n";
    fixtures[runtime].text = "  4.11.3 \r\n";
    fixtures[clock].text =
        " unrelated 0 0 0 123\n"
        " clk_c906_0 1 1 0 1000000000\n"
        " clk_tpu 1 1 0 500000000\n";
    for (int i = 0; i != 256; ++i) {
        assert(maix::sys::os_version() == "NanoKVM OS");
        assert(maix::sys::device_key() == "fixture-key");
        assert(maix::sys::runtime_version() == "4.11.3");
        assert(maix::sys::cpu_freq().at("cpu0") == 1000000000UL);
        assert(maix::sys::npu_freq().at("npu0") == 500000000UL);
        balanced();
    }
    fixtures[clock].text = " clk_c906_0 invalid\n clk_tpu invalid\n unrelated\n";
    assert(maix::sys::cpu_freq().empty());
    assert(maix::sys::npu_freq().empty());
    fixtures[runtime].text = " \t\r\n";
    assert(maix::sys::runtime_version().empty());
    balanced();
    for (auto &item : fixtures)
        item.second.read_error = true;
    assert(maix::sys::os_version() == "Unkonwn");
    assert(maix::sys::device_key().empty());
    assert(maix::sys::runtime_version().empty());
    assert(maix::sys::cpu_freq().empty());
    assert(maix::sys::npu_freq().empty());
    balanced();
    for (auto &item : fixtures)
        item.second.missing = true;
    assert(maix::sys::os_version() == "Unkonwn");
    assert(maix::sys::device_key().empty());
    assert(maix::sys::runtime_version().empty());
    assert(maix::sys::cpu_freq().empty());
    assert(maix::sys::npu_freq().empty());
    balanced();
    printf("PASS: empty, successful, malformed, whitespace, read-error and missing files; %u streams closed\n",
           closed);
}
