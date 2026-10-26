/* Short-record warm-key benchmark, sharing the independent AEAD harness. */
#define main c906_original_main
#include "../openssl-c906/evp-bench.c"
#undef main
int main(int argc,char **argv){const char *name=argc>1?argv[1]:"CHACHA20-POLY1305";double seconds=argc>2?strtod(argv[2],0):0.05;ck(seconds>0&&seconds<=1,"duration");int sizes[]={16,32,64,128,256,319,320,512,640,768,1024,1400,1536,2048,3072,4096,16384};for(unsigned i=0;i<sizeof sizes/sizeof *sizes;i++)for(int enc=0;enc<2;enc++)aead_bench(name,enc,sizes[i],5,(uint64_t)(seconds*1e9));struct rusage r;ck(getrusage(RUSAGE_SELF,&r)==0,"rusage");printf("{\"kind\":\"memory\",\"maxrss_kib\":%ld}\n",r.ru_maxrss);return 0;}
