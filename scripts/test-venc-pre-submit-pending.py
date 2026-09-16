#!/usr/bin/env python3
"""Exercise the real vendor pre-submit validation path with host stubs."""
import argparse
from pathlib import Path
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--source", type=Path, required=True)
args = parser.parse_args()
s = args.source.read_text()
a = s.index("RetCode VPU_EncStartOneFrame(")
b = s.index("\nRetCode ", a + 20)
function = s[a:b]
stubs = r"""
#include <assert.h>
#include <stddef.h>
#include <stdio.h>
typedef int RetCode;
enum { RETCODE_SUCCESS, RETCODE_INVALID_HANDLE, RETCODE_WRONG_CALL_SEQUENCE, RETCODE_INVALID_PARAM, TRUE=1 };
typedef struct { int stride; unsigned long ptsMap[8]; struct { int enablePTS; } openParam; } EncInfo;
typedef struct { EncInfo encInfo; } CodecInfo;
typedef struct CodecInst { CodecInfo *CodecInfo; int coreIdx; struct { int frameNo; } cvi; } CodecInst;
typedef CodecInst *EncHandle;
typedef struct { int srcIdx; unsigned long pts; } EncParam;
typedef int VpuAttr;
typedef int vpu_instance_pool_t;
static VpuAttr g_VpuCoreAttributes[1];
static vpu_instance_pool_t pool;
static CodecInst *pending;
static int validation, submitted;
#define CVI_VC_ERR(...) ((void)0)
static RetCode CheckEncInstanceValidity(EncHandle h) { return h ? RETCODE_SUCCESS : RETCODE_INVALID_HANDLE; }
static void *vdi_get_instance_pool(int core) { (void)core; return &pool; }
static CodecInst *GetPendingInst(int core) { (void)core; return pending; }
static void SetPendingInst(int core, CodecInst *inst, const char *fn, int line) { (void)core;(void)fn;(void)line;pending=inst; }
static RetCode CheckEncParam(EncHandle h, EncParam *p) { (void)h;(void)p;return validation; }
static unsigned long GetTimestamp(EncHandle h) { (void)h;return 123; }
static RetCode ProductVpuEncode(CodecInst *inst, EncParam *p) { (void)inst;(void)p;submitted++;return RETCODE_SUCCESS; }
"""
tests = r"""
int main(void) {
 CodecInfo info={0}; CodecInst inst={.CodecInfo=&info}, other={0}; EncParam param={0};
 pending=&inst;
 assert(VPU_EncStartOneFrame(&inst,&param)==RETCODE_WRONG_CALL_SEQUENCE);
 assert(pending==NULL && submitted==0);
 pending=&other;
 assert(VPU_EncStartOneFrame(&inst,&param)==RETCODE_WRONG_CALL_SEQUENCE);
 assert(pending==&other && submitted==0);
 info.encInfo.stride=1920;validation=RETCODE_INVALID_PARAM;pending=&inst;
 assert(VPU_EncStartOneFrame(&inst,&param)==RETCODE_INVALID_PARAM);
 assert(pending==NULL && submitted==0);
 validation=RETCODE_SUCCESS;pending=&inst;
 assert(VPU_EncStartOneFrame(&inst,&param)==RETCODE_SUCCESS);
 assert(pending==&inst && submitted==1);
 puts("PASS: failed pre-submit clears only its own pending instance; valid submit retains ownership");
}
"""
with tempfile.TemporaryDirectory() as td:
 source=Path(td)/"test.c"; binary=Path(td)/"test"
 source.write_text(stubs+function+tests)
 subprocess.run(["cc","-Wall","-Wextra","-Werror",str(source),"-o",str(binary)],check=True)
 subprocess.run([str(binary)],check=True)
