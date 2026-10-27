#define _GNU_SOURCE
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdarg.h>
#include <openssl/crypto.h>
static const char *option;
static int control, vector_calls, scalar_calls, controls;
int ossl_riscv_has_xtheadvector;
static char *environment(const char *name)
{
    assert(!strcmp(name,"NANOKVM_OPENSSL_GHASH"));
    return (char *)option;
}
static int thread_control(int op, ...)
{
    assert(op == 70); ++controls; return control;
}
#define secure_getenv environment
#define prctl thread_control
#include "gcm_c906.c"
#undef secure_getenv
#undef prctl
void ossl_ghash_blocks_c906(u64 acc[2], const void *kp, const u8 *in, size_t blocks)
{
    (void)acc; (void)kp; (void)in; assert(blocks == 1); ++scalar_calls;
}
void ossl_ghash_blocks_c906_v(u64 acc[2], const void *kp, const u8 *in, size_t groups)
{
    (void)acc; (void)kp; (void)in; assert(groups != 0); ++vector_calls;
}
static void reset(int capable, const char *opt, int ctl)
{
    ossl_riscv_has_xtheadvector = capable; option = opt; control = ctl;
    vector_calls = scalar_calls = controls = disabled = 0;
    once = (CRYPTO_ONCE)CRYPTO_ONCE_STATIC_INIT;
}
int main(void)
{
    u128 table[16]={{0}},copy[16]; u64 xi[2]={0}; u8 input[16400]={0};
    reset(0,NULL,2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!vector_calls && !scalar_calls && !controls);
    reset(1,"generic",2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    for(int ctl=-1;ctl<2;ctl++) {
        reset(1,NULL,ctl); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
        assert(!vector_calls && !scalar_calls);
    }
    reset(1,NULL,2);
    assert(ossl_gcm_c906_prefix(xi,table,input,3071)==0 && !controls);
    assert(ossl_gcm_c906_prefix(xi,table,input+5,3072)==3040);
    assert(vector_calls==1 && scalar_calls==4 && controls==1);
    memcpy(copy,table,sizeof(copy));
    control=1; assert(ossl_gcm_c906_prefix(xi,copy,input,16384)==0);
    assert(vector_calls==1 && controls==2);
    control=2; assert(ossl_gcm_c906_prefix(xi,copy,input,16384)==16320);
    assert(vector_calls==2 && scalar_calls==8 && controls==3);
    puts("PASS: hwprobe/override/thread gates, short input, temporary key and copied context dispatch");
    return 0;
}
