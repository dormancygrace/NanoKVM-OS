/* SPDX-License-Identifier: GPL-2.0-only */
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include "hwcrypto.h"
int main(void){
 int fd=open("/dev/nanokvm-hwcrypto",O_RDONLY|O_CLOEXEC);struct hwcrypto_info i;
 if(fd<0||ioctl(fd,HWCRYPTO_INFO,&i)){perror("CryptoDMA info");return 1;}close(fd);
 printf("{\"abi\":%u,\"algorithms\":%u,\"poisoned\":%u,\"completed\":[",i.version,i.algorithms,i.poisoned);
 for(unsigned n=0;n<HWCRYPTO_ALG_COUNT;n++)printf("%s%llu",n?",":"",(unsigned long long)i.completed[n]);
 puts("]}");return i.poisoned?1:0;
}
