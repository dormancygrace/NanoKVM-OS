#define main c906_original_main
#include "../openssl-c906/evp-bench.c"
#undef main
static void mem(const char *phase){char line[256];FILE *f=fopen("/proc/self/smaps_rollup","r");ck(f!=NULL,"smaps");printf("MEM phase=%s\n",phase);while(fgets(line,sizeof line,f))if(!strncmp(line,"Rss:",4)||!strncmp(line,"Anonymous:",10)||!strncmp(line,"Private_Dirty:",14))fputs(line,stdout);fclose(f);}
int main(int argc,char **argv){
 const char *name=argc>1?argv[1]:"AES-128-GCM";EVP_CIPHER *c=EVP_CIPHER_fetch(NULL,name,NULL);ck(c!=NULL,"fetch");
 EVP_CIPHER_CTX *ctx=EVP_CIPHER_CTX_new();unsigned char key[32]={0},iv[12]={0},aad[5]={0},in[4096]={0},out[4112],tag[16];
 int sizes[]={16,64,256,1024,4096};for(unsigned z=0;z<sizeof sizes/sizeof *sizes;z++){
  struct rusage r0,r1;getrusage(RUSAGE_SELF,&r0);uint64_t w=ns(CLOCK_MONOTONIC),c0=ns(CLOCK_PROCESS_CPUTIME_ID),c1,n=0;
  do{for(int j=0;j<32;j++){key[0]=(unsigned char)n;ck(crypt(ctx,c,1,key,iv,aad,5,in,sizes[z],out,tag,0),"cold encrypt");n++;sink^=tag[0];}c1=ns(CLOCK_PROCESS_CPUTIME_ID);}while(c1-c0<30000000);
  getrusage(RUSAGE_SELF,&r1);output(name,"new-key",sizes[z],0,n,c1-c0,ns(CLOCK_MONOTONIC)-w,&r0,&r1);
 }
 mem("before");EVP_CIPHER_CTX *many[256];for(unsigned i=0;i<256;i++){many[i]=EVP_CIPHER_CTX_new();ck(many[i]!=NULL&&EVP_EncryptInit_ex2(many[i],c,key,iv,NULL)==1,"many");}mem("256-contexts");for(unsigned i=0;i<256;i++)EVP_CIPHER_CTX_free(many[i]);EVP_CIPHER_CTX_free(ctx);EVP_CIPHER_free(c);return 0;
}
