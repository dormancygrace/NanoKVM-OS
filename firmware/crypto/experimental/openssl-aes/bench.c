#define _POSIX_C_SOURCE 200809L
#include <openssl/evp.h>
#include <openssl/provider.h>
#include <time.h>
#include <sys/resource.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
static void ck(int x){if(!x){fputs("FAIL EVP benchmark\n",stderr);exit(2);}}
static uint64_t ns(clockid_t id){struct timespec t;ck(clock_gettime(id,&t)==0);return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;}
int main(int argc,char **argv){
 ck(argc>=2);double duration=argc>2?strtod(argv[2],0):0.1;ck(duration>0&&duration<=2);
 OSSL_PROVIDER *def=OSSL_PROVIDER_load(0,"default"),*legacy=OSSL_PROVIDER_load(0,"legacy");ck(def&&legacy);
 EVP_CIPHER *cipher=EVP_CIPHER_fetch(0,argv[1],0);EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();ck(cipher&&ctx);
 unsigned char key[32]={0},iv[16]={0},input[65568],output[65568];memset(input,0x37,sizeof input);
 int sizes[]={16,32,64,128,256,512,768,1024,1280,1536,1792,2048,2304,2560,2816,3072,3584,4096,4112,4160,4352,4608,4864,5120,5376,5632,6000,6144,6400,6656,8192,16384};
 for(unsigned i=0;i<sizeof sizes/sizeof sizes[0];i++)for(int enc=0;enc<=1;enc++){
  uint64_t cpu=ns(CLOCK_PROCESS_CPUTIME_ID),wall=ns(CLOCK_MONOTONIC),rounds=0,end;int n,m;
  do{for(int k=0;k<16;k++){
   ck(EVP_CipherInit_ex2(ctx,cipher,key,iv,enc,0)==1);ck(EVP_CIPHER_CTX_set_padding(ctx,0)==1);
   ck(EVP_CipherUpdate(ctx,output+5,&n,input+5,sizes[i])==1);ck(EVP_CipherFinal_ex(ctx,output+5+n,&m)==1&&n+m==sizes[i]);rounds++;
  }end=ns(CLOCK_PROCESS_CPUTIME_ID);}while(end-cpu<(uint64_t)(duration*1e9));
  wall=ns(CLOCK_MONOTONIC)-wall;cpu=end-cpu;
  printf("{\"algorithm\":\"%s\",\"encrypt\":%d,\"bytes\":%d,\"rounds\":%llu,\"cpu_ns\":%llu,\"wall_ns\":%llu,\"mib_per_cpu_s\":%.6f}\n",argv[1],enc,sizes[i],(unsigned long long)rounds,(unsigned long long)cpu,(unsigned long long)wall,(double)sizes[i]*rounds*1e9/(cpu*1048576.0));
 }
 struct rusage usage;ck(getrusage(RUSAGE_SELF,&usage)==0);printf("{\"kind\":\"memory\",\"maxrss_kib\":%ld}\n",usage.ru_maxrss);
 EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(cipher);OSSL_PROVIDER_unload(legacy);OSSL_PROVIDER_unload(def);return 0;
}
