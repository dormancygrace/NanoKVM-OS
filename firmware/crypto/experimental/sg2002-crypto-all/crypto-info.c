#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include "sg2002_crypto.h"
int main(void)
{
 int fd=open("/dev/sg2002-aes-probe",O_RDONLY|O_CLOEXEC), extended=1;
 struct sg2002_crypto_info2 v={0};
 if(fd<0){perror("CryptoDMA open");return 1;}
 if(ioctl(fd,SG2002_CRYPTO_INFO2,&v)){
  if(errno!=ENOTTY || ioctl(fd,SG2002_CRYPTO_INFO,&v.legacy)){perror("CryptoDMA info");close(fd);return 1;}
  extended=0;
 }
 close(fd);
 printf("{\"version\":%u,\"algorithms\":%u,\"poisoned\":%u,\"completed\":[",v.legacy.version,v.legacy.algorithms,v.legacy.poisoned);
 for(unsigned n=0;n<SG2002_INFO_V1_COUNT;n++)printf("%s%llu",n?",":"",(unsigned long long)v.legacy.completed[n]);
 if(extended)printf(",%llu",(unsigned long long)v.sm3_completed);
 puts("]}");return 0;
}
