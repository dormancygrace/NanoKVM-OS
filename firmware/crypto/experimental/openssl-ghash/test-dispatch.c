#define _GNU_SOURCE
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdarg.h>
#include <sys/prctl.h>
#include <openssl/crypto.h>
static const char *option;
static int vector_calls, scalar_calls, controls;
int ossl_riscv_has_xtheadvector;
static char *environment(const char *name)
{
    assert(!strcmp(name,"NANOKVM_OPENSSL_GHASH"));
    return (char *)option;
}
/* riscvcap.c checks the vector state once at load; the hash path must not. */
__attribute__((unused)) static int thread_control(int op, ...)
{
    (void)op; ++controls; return -1;
}
#define secure_getenv environment
#define prctl thread_control
#include "gcm_c906.c"
#undef secure_getenv
#undef prctl
void ossl_ghash_blocks_c906(u64 acc[2], const void *kp, const u8 *in, size_t blocks)
{
    (void)acc; (void)kp; (void)in; assert(blocks >= 1 && blocks <= 4); ++scalar_calls;
}
void ossl_ghash_blocks_c906_v(u64 acc[2], const void *kp, const u8 *in, size_t groups)
{
    (void)acc; (void)kp; (void)in; assert(groups != 0); ++vector_calls;
}
static void reset(int capable, const char *opt)
{
    ossl_riscv_has_xtheadvector = capable; option = opt;
    vector_calls = scalar_calls = controls = disabled = 0;
    once = (CRYPTO_ONCE)CRYPTO_ONCE_STATIC_INIT;
}
int main(void)
{
    u128 table[16]={{0}},copy[16]; u64 xi[2]={0}; u8 input[16400]={0};
    /* No XTheadVector, or vector state off when libcrypto was loaded. */
    reset(0,NULL); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!vector_calls && !scalar_calls);
    reset(1,"generic"); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!vector_calls);
    /* No variable, the former opt-in or any other value: the default. */
    reset(1,NULL); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==16384);
    reset(1,"auto"); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==16384);
    reset(1,"unknown"); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==16384);
    reset(1,NULL);
    assert(ossl_gcm_c906_prefix(xi,table,input,2047)==0);
    assert(ossl_gcm_c906_prefix(xi,table,input+5,2048)==2048);
    assert(vector_calls==1 && scalar_calls==5);
    memcpy(copy,table,sizeof(copy));
    assert(ossl_gcm_c906_prefix(xi,copy,input,16384)==16384);
    assert(vector_calls==2 && scalar_calls==10);
    assert(!controls);
    puts("PASS: load-time capability, generic override, no syscall on the hash path, short input, temporary key and copied context dispatch");
    return 0;
}
