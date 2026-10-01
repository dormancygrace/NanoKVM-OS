#ifndef NANOKVM_FILE_STAMP_HPP
#define NANOKVM_FILE_STAMP_HPP

#include <cstdint>
#include <sys/stat.h>

namespace nanokvm {
// Adapted from IronKVM 8244b7f3 (yuzi-co). This stamp is owned by the HDMI
// detection thread; it is not a synchronization primitive.
struct FileStamp {
    bool valid = false;
    uint64_t device = 0;
    uint64_t inode = 0;
    int64_t size = 0;
    int64_t mtime_sec = 0;
    int64_t mtime_nsec = 0;
    uint32_t read_ms = 0;
};

inline bool file_stamp_due(const FileStamp &stamp, const struct stat &st,
    uint32_t now_ms, uint32_t max_age_ms) {
    return !stamp.valid || stamp.device != static_cast<uint64_t>(st.st_dev)
        || stamp.inode != static_cast<uint64_t>(st.st_ino)
        || stamp.size != static_cast<int64_t>(st.st_size)
        || stamp.mtime_sec != static_cast<int64_t>(st.st_mtim.tv_sec)
        || stamp.mtime_nsec != static_cast<int64_t>(st.st_mtim.tv_nsec)
        // Unsigned subtraction also handles the monotonic clock's wraparound.
        || static_cast<uint32_t>(now_ms - stamp.read_ms) >= max_age_ms;
}

inline void file_stamp_record(FileStamp &stamp, const struct stat &st,
    uint32_t now_ms) {
    stamp.valid = true;
    stamp.device = static_cast<uint64_t>(st.st_dev);
    stamp.inode = static_cast<uint64_t>(st.st_ino);
    stamp.size = static_cast<int64_t>(st.st_size);
    stamp.mtime_sec = static_cast<int64_t>(st.st_mtim.tv_sec);
    stamp.mtime_nsec = static_cast<int64_t>(st.st_mtim.tv_nsec);
    stamp.read_ms = now_ms;
}
} // namespace nanokvm
#endif
