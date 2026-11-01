#define _POSIX_C_SOURCE 200809L
#include <stdint.h>
#include <stdio.h>
#include <time.h>
#include <stdlib.h>
#define N 4096
static uint32_t a[N], b[N], c[N];
static float x[N], y[N];
static double now(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC, &t); return t.tv_sec+t.tv_nsec/1e9; }
int main(int argc,char **argv) {
 unsigned rounds=argc>1?strtoul(argv[1],0,10):10000;
 for(unsigned i=0;i<N;i++){a[i]=i*2654435761u;b[i]=i*17+23;c[i]=i;x[i]=(float)(i%251)/256;y[i]=(float)(i%17)/32;}
 double t=now();
 for(unsigned r=0;r<rounds;r++) for(unsigned i=0;i<N;i++) a[i]=(a[i]*33u+b[i])^(c[i]>>3);
 double integer=now()-t;
 t=now();
 for(unsigned r=0;r<rounds;r++) for(unsigned i=0;i<N;i++) x[i]=x[i]*0.999f+y[i]*0.001f;
 double fp=now()-t;
 uint64_t sum=0;double fsum=0;
 for(unsigned i=0;i<N;i++){sum+=a[i];fsum+=x[i];}
 printf("integer_s=%.6f float_s=%.6f checksum=%llu fsum=%.9f\n",integer,fp,(unsigned long long)sum,fsum);
 return 0;
}
