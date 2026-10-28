// SPDX-License-Identifier: GPL-3.0-only
// Stock SD migration: stage new filesystems wholly inside the old root, then
// change one MBR sector. The old boot and the data partition are never written.
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <inttypes.h>
#include <linux/fs.h>
#include <linux/loop.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/stat.h>
#include <sys/sysmacros.h>
#include <unistd.h>

#define SECTOR 512U
#define ALIGN 8192U
#define BOOT_SECTORS 131072U
#define MIN_ROOT 1572864U
struct layout { uint32_t start[3], size[3], boot, root, rootsize; };
static void die(const char *s) { fprintf(stderr, "stock-layout: %s\n", s); exit(1); }
static uint32_t get32(const unsigned char *p) {
    return (uint32_t)p[0] | (uint32_t)p[1]<<8 | (uint32_t)p[2]<<16 | (uint32_t)p[3]<<24;
}
static void put32(unsigned char *p, uint32_t v) {
    for (int i=0; i<4; i++) p[i]=(unsigned char)(v>>(i*8));
}
static void read_sector(int fd, unsigned char *m) {
    if (pread(fd,m,SECTOR,0)!=SECTOR) die("cannot read MBR");
}
static struct layout plan(const unsigned char *m, uint64_t sectors) {
    struct layout l={0};
    if (m[510]!=0x55 || m[511]!=0xaa) die("not an MBR card");
    for (int i=0; i<3; i++) {
        const unsigned char *p=m+446+16*i;
        l.start[i]=get32(p+8); l.size[i]=get32(p+12);
        if (!l.start[i] || !l.size[i] || (uint64_t)l.start[i]+l.size[i]>sectors)
            die("partition outside the card");
        if (p[0]!=0 && p[0]!=0x80) die("invalid active flag");
        if (i && l.start[i]<(uint64_t)l.start[i-1]+l.size[i-1])
            die("overlapping or unordered partitions");
    }
    for (int i=494; i<510; i++) if (m[i]) die("a fourth partition is unsupported");
    if (m[450]!=0x06 && m[450]!=0x0e && m[450]!=0x0b && m[450]!=0x0c)
        die("partition 1 is not FAT");
    if (m[466]!=0x83 || (m[482]!=0x83 && m[482]!=0x07))
        die("expected a Linux root and an exFAT data partition");
    uint64_t boot=((uint64_t)l.start[1]+ALIGN-1)/ALIGN*ALIGN;
    uint64_t root=boot+BOOT_SECTORS;
    uint64_t end=(uint64_t)l.start[1]+l.size[1];
    if (root>UINT32_MAX || end<root+MIN_ROOT) die("old root lacks room for a 64 MiB boot and 768 MiB root");
    l.boot=(uint32_t)boot; l.root=(uint32_t)root; l.rootsize=(uint32_t)(end-root);
    return l;
}
static void entry(unsigned char *p, unsigned char active, unsigned char type,
                  uint32_t start, uint32_t size) {
    memset(p,0,16); p[0]=active; p[4]=type;
    // Saturated CHS: all boot and kernel consumers use LBA.
    p[1]=p[5]=0xfe; p[2]=p[3]=p[6]=p[7]=0xff;
    put32(p+8,start); put32(p+12,size);
}
static void replacement(unsigned char *m, struct layout l) {
    entry(m+446,0x80,0x0e,l.boot,BOOT_SECTORS);
    entry(m+462,0,0x83,l.root,l.rootsize);
}
static uint64_t capacity(int fd, int fixture) {
    struct stat st;
    if (fstat(fd,&st)) die("stat card failed");
    if (fixture && S_ISREG(st.st_mode) && st.st_size>0 && st.st_size%SECTOR==0)
        return (uint64_t)st.st_size/SECTOR;
    if (!S_ISBLK(st.st_mode)) die("card must be a block device");
    unsigned logical=0;
    uint64_t bytes=0;
    if (ioctl(fd,BLKSSZGET,&logical) || logical!=SECTOR || ioctl(fd,BLKGETSIZE64,&bytes))
        die("expected a 512-byte-sector card");
    return bytes/SECTOR;
}
static void map_partition(int fd, uint64_t start, uint64_t size) {
    int ctl=open("/dev/loop-control",O_RDWR|O_CLOEXEC);
    if (ctl<0) die("open loop-control failed");
    int number=ioctl(ctl,LOOP_CTL_GET_FREE);
    close(ctl);
    if (number<0) die("no free loop device");
    char path[64];
    snprintf(path,sizeof(path),"/dev/loop%d",number);
    if (mknod(path,S_IFBLK|0600,makedev(7,number)) && errno!=EEXIST) die("create loop device failed");
    int loop=open(path,O_RDWR|O_CLOEXEC|O_NOFOLLOW);
    if (loop<0) die("open loop device failed");
    struct loop_config config={0};
    config.fd=(uint32_t)fd;
    config.info.lo_offset=start*SECTOR;
    config.info.lo_sizelimit=size*SECTOR;
    // Leave attached until explicit detach/reboot; this process exits before mount.
    if (ioctl(loop,LOOP_CONFIGURE,&config)) die("bounded loop mapping failed");
    uint64_t actual=0;
    if (ioctl(loop,BLKGETSIZE64,&actual) || actual!=size*SECTOR) {
        (void)ioctl(loop,LOOP_CLR_FD,0); die("loop size differs from partition plan");
    }
    close(loop); puts(path);
}
int main(int argc, char **argv) {
    if (argc<3) die("usage: stock-layout plan|save|state|map-boot|map-root|commit CARD [SAVED-MBR]");
    const char *op=argv[1];
    int fixture=0;
#ifdef STOCK_LAYOUT_TEST
    fixture=1;
#else
    if (strcmp(argv[2],"/dev/mmcblk0")) die("only /dev/mmcblk0 is supported");
#endif
    int writing=!strcmp(op,"commit") || !strcmp(op,"map-boot") || !strcmp(op,"map-root");
    int fd=open(argv[2],(writing?O_RDWR:O_RDONLY)|O_CLOEXEC|O_NOFOLLOW);
    if (fd<0) die("open card failed");
    uint64_t sectors=capacity(fd,fixture);
    unsigned char current[SECTOR],saved[SECTOR],next[SECTOR];
    read_sector(fd,current);
    if (!strcmp(op,"plan") || !strcmp(op,"save")) {
        if (argc!=3) die("unexpected argument");
        struct layout l=plan(current,sectors);
        if (!strcmp(op,"save")) {
            if (fwrite(current,1,SECTOR,stdout)!=SECTOR) die("cannot save MBR");
        } else {
            printf("%u %u %u %u %u %u %u %u %u\n",l.start[0],l.size[0],
                   l.start[1],l.size[1],l.start[2],l.size[2],l.boot,l.root,l.rootsize);
        }
    } else {
        if (argc!=4) die("saved MBR required");
        int s=open(argv[3],O_RDONLY|O_CLOEXEC|O_NOFOLLOW);
        struct stat st;
        if (s<0 || fstat(s,&st) || !S_ISREG(st.st_mode) || st.st_size!=SECTOR)
            die("saved MBR must be a 512-byte regular file");
        read_sector(s,saved); close(s);
        struct layout l=plan(saved,sectors);
        memcpy(next,saved,SECTOR); replacement(next,l);
        int old=!memcmp(current,saved,SECTOR), done=!memcmp(current,next,SECTOR);
        if (!old && !done) die("partition table changed since staging");
        if (!strcmp(op,"state")) puts(old?"stock":"installed");
        else if (!strcmp(op,"commit")) {
            if (old && (pwrite(fd,next,SECTOR,0)!=SECTOR || fsync(fd))) die("MBR commit failed");
            struct stat card;
            if (fstat(fd,&card)) die("stat card failed");
            // Drop the block-device cache after fsync: read the sector back
            // from the device instead of comparing our own cached write.
            if (S_ISBLK(card.st_mode) && ioctl(fd,BLKFLSBUF,0)) die("MBR cache flush failed");
            read_sector(fd,current);
            if (memcmp(current,next,SECTOR)) die("MBR readback failed");
        } else if (!strcmp(op,"map-boot") || !strcmp(op,"map-root")) {
            if (!old) die("refusing to remap an installed system");
            if (!strcmp(op,"map-boot")) map_partition(fd,l.boot,BOOT_SECTORS);
            else map_partition(fd,l.root,l.rootsize);
        } else die("unknown command");
    }
    if (close(fd)) die("close card failed");
    return 0;
}
