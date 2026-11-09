#define _GNU_SOURCE
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdarg.h>
#include <pthread.h>
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
    assert(op == 70); __atomic_add_fetch(&controls, 1, __ATOMIC_SEQ_CST); return control;
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
static void reset(int capable, const char *opt, int ctl)
{
    ossl_riscv_has_xtheadvector = capable; option = opt; control = ctl;
    vector_calls = scalar_calls = controls = disabled = 0;
    once = (CRYPTO_ONCE)CRYPTO_ONCE_STATIC_INIT; ossl_riscv_c906_vstate = 0;
}
static u128 shared_table[16]; static u8 shared_input[2048];
static void *other_thread(void *arg)
{
    u64 xi[2]={0}; *(size_t *)arg=ossl_gcm_c906_prefix(xi,shared_table,shared_input,2048); return NULL;
}
int main(void)
{
    u128 table[16]={{0}},copy[16]; u64 xi[2]={0}; u8 input[16400]={0};
    reset(0,"auto",2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!vector_calls && !scalar_calls && !controls);
    reset(1,"generic",2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!controls);
    reset(1,NULL,2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!controls);
    reset(1,"unknown",2); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!controls);
    for(int ctl=-1;ctl<2;ctl++) {
        reset(1,"auto",ctl); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
        assert(!vector_calls && !scalar_calls);
    }
    reset(1,"auto",2);
    assert(ossl_gcm_c906_prefix(xi,table,input,2047)==0 && !controls);
    assert(ossl_gcm_c906_prefix(xi,table,input+5,2048)==2048);
    assert(vector_calls==1 && scalar_calls==5 && controls==1);
    /* One query per thread: a copied context in the same thread asks no more. */
    memcpy(copy,table,sizeof(copy));
    assert(ossl_gcm_c906_prefix(xi,copy,input,16384)==16384);
    assert(vector_calls==2 && scalar_calls==10 && controls==1);
    /* "Off" is kept: the thread stays generic after enabling vectors. */
    reset(1,"auto",1); assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    control=2; assert(ossl_gcm_c906_prefix(xi,table,input,16384)==0);
    assert(!vector_calls && controls==1);
    /* Another thread asks for itself. */
    reset(1,"auto",2); assert(ossl_gcm_c906_prefix(xi,table,input,2048)==2048 && controls==1);
    pthread_t t; size_t got=0;
    assert(pthread_create(&t,NULL,other_thread,&got)==0 && pthread_join(t,NULL)==0);
    assert(got==2048 && controls==2);
    puts("PASS: hwprobe/opt-in/thread gates, one query per thread, short input, temporary key and copied context dispatch");
    return 0;
}
