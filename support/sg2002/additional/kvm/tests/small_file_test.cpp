#include <cerrno>
#include <cstdio>
#include <cstring>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>
#include <cassert>
#include <cstdlib>

static int calls;
static ssize_t short_write(int fd, const void *buf, size_t count) {
    if (++calls == 1) { errno = EINTR; return -1; }
    return ::write(fd, buf, count > 2 ? 2 : count);
}
#define write short_write
#include "internal/small_file.hpp"
#undef write

int main() {
    char path[] = "/tmp/nanokvm-small-file-XXXXXX";
    int fd=mkstemp(path);assert(fd>=0);assert(fchmod(fd,0600)==0);close(fd);
    assert(nanokvm::write_small_uint(path, 1080, true));
    assert(calls>=4); // EINTR and multiple successful short writes.
    char text[32]={};fd=open(path,O_RDONLY);assert(read(fd,text,sizeof(text))==5);close(fd);
    assert(strcmp(text,"1080\n")==0);
    assert(nanokvm::write_small_uint(path,3));
    struct stat st;assert(stat(path,&st)==0);assert(st.st_size==2);assert((st.st_mode&0777)==0600);
    assert(!nanokvm::write_small_file("/dev/full","fail\n"));
    assert(!nanokvm::write_small_file("/nonexistent/nanokvm/file","fail\n"));
    unlink(path);
    puts("small file: EINTR, short writes, truncation, permissions, durability and errors PASS");
}
