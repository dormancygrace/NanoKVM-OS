#define _GNU_SOURCE
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdarg.h>
#include <sys/prctl.h>
#include <openssl/crypto.h>
static int vector_calls, scalar_calls, controls;
/* The kernel keeps vectors on for every thread (0078): no query on the path. */
__attribute__((unused)) static int thread_control(int op, ...)
{
    (void)op; ++controls; return -1;
}
#define prctl thread_control
#include "gcm_c906.c"
#undef prctl
void ossl_ghash_blocks_c906(u64 acc[2], const void *kp, const u8 *in, size_t blocks)
{
    (void)acc; (void)kp; (void)in; assert(blocks >= 1 && blocks <= 4); ++scalar_calls;
}
void ossl_ghash_blocks_c906_v(u64 acc[2], const void *kp, const u8 *in, size_t groups)
{
    (void)acc; (void)kp; (void)in; assert(groups != 0); ++vector_calls;
}
int main(void)
{
    u128 table[16]={{0}},copy[16]; u64 xi[2]={0},kp[112]={0},kpcopy[112]; u8 input[16400]={0};
    assert(ossl_gcm_c906_prefix(xi,table,kp,input,2047)==0 && !kp[0]);
    /* First use builds the key: four powers, then the 3-block tail. */
    assert(ossl_gcm_c906_prefix(xi,table,kp,input+5,2048)==2048);
    assert(vector_calls==1 && scalar_calls==5 && kp[0]);
    /* Cached key: only the tail call. */
    assert(ossl_gcm_c906_prefix(xi,table,kp,input,16384)==16384);
    assert(vector_calls==2 && scalar_calls==6);
    memcpy(copy,table,sizeof(copy));memcpy(kpcopy,kp,sizeof(kpcopy));
    assert(ossl_gcm_c906_prefix(xi,copy,kpcopy,input,16384)==16384);
    assert(vector_calls==3 && scalar_calls==7);
    assert(!controls);
    puts("PASS: hwprobe capability, no syscall, short input, key built once per context, copied context dispatch");
    return 0;
}
