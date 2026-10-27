/* SPDX-License-Identifier: Apache-2.0 */
#define main c906_baseline_main
#include "../openssl-c906/evp-bench.c"
#undef main
int main(int argc,char **argv)
{
    const char *name=argc>1?argv[1]:"SHA256";double seconds=argc>2?strtod(argv[2],NULL):0.2;
    ck(seconds>0&&seconds<=2,"duration");
    const int sizes[]={64,256,512,1024,2048,3072,4095,4096,4097,8192,16384};
    for(size_t i=0;i<sizeof sizes/sizeof sizes[0];i++)digest_bench(name,sizes[i],(uint64_t)(seconds*1e9));
    struct rusage r;ck(getrusage(RUSAGE_SELF,&r)==0,"rusage");
    printf("{\"kind\":\"memory\",\"maxrss_kib\":%ld}\n",r.ru_maxrss);return 0;
}
