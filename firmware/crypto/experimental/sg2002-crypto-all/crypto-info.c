/* SPDX-License-Identifier: GPL-2.0-only */
#include <stdio.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include "sg2002_crypto.h"
int main(void){
 int fd=open("/dev/sg2002-aes-probe",O_RDONLY|O_CLOEXEC);struct sg2002_crypto_info i;
 if(fd<0||ioctl(fd,SG2002_CRYPTO_INFO,&i)){perror("CryptoDMA info");return 1;}close(fd);
 printf("{\"abi\":%u,\"algorithms\":%u,\"poisoned\":%u,\"completed\":[",i.version,i.algorithms,i.poisoned);
 for(unsigned n=0;n<SG2002_ALG_COUNT;n++)printf("%s%llu",n?",":"",(unsigned long long)i.completed[n]);
 puts("]}");return i.poisoned?1:0;
}
