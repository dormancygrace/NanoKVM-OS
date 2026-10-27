/* SPDX-License-Identifier: Apache-2.0 */
#define _POSIX_C_SOURCE 200809L
#define OPENSSL_SUPPRESS_DEPRECATED
#include <openssl/aes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>
static volatile unsigned sink;
static uint64_t ns(void){struct timespec t;if(clock_gettime(CLOCK_PROCESS_CPUTIME_ID,&t))abort();return (uint64_t)t.tv_sec*1000000000+t.tv_nsec;}
int main(void){unsigned char key[32],input[16],out[16];AES_KEY ek,dk,temp;for(unsigned i=0;i<32;i++)key[i]=(unsigned char)(i*7+3);for(unsigned i=0;i<16;i++)input[i]=(unsigned char)(i*13+21);
 const char *names[]={"set-encrypt-key","set-decrypt-key","encrypt-block","decrypt-block"};
 for(int bits=128;bits<=256;bits+=64){if(AES_set_encrypt_key(key,bits,&ek)||AES_set_decrypt_key(key,bits,&dk))abort();
  for(unsigned mode=0;mode<4;mode++){uint64_t start=ns(),end,n=0;
   do{for(unsigned i=0;i<256;i++){if(mode<2){int rc=mode==0?AES_set_encrypt_key(key,bits,&temp):AES_set_decrypt_key(key,bits,&temp);if(rc)abort();sink^=temp.rd_key[0];}else{if(mode==2)AES_encrypt(input,out,&ek);else AES_decrypt(input,out,&dk);sink^=out[0];}n++;}end=ns();}while(end-start<30000000);
   printf("{\"bits\":%d,\"operation\":\"%s\",\"iterations\":%llu,\"cpu_ns_per_call\":%.3f}\n",bits,names[mode],(unsigned long long)n,(double)(end-start)/n);
  }
 }return 0;}
