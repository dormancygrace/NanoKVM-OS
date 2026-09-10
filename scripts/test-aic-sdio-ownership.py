#!/usr/bin/env python3
"""Compile and exercise the actual BSP ID helper and dummy probe with host stubs."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
src = a.source.resolve() / 'aic8800_bsp'
s = (src / 'aicsdio.c').read_text()
start = s.index('static int aicbsp_dummy_probe(')
brace = s.index('{', start)
depth = 1
end = brace + 1
while depth:
    if s[end] == '{': depth += 1
    elif s[end] == '}': depth -= 1
    end += 1
probe = s[start:end]
defines = '\n'.join(l for l in s.splitlines() if l.startswith('#define SDIO_VENDOR_ID_AIC') or l.startswith('#define SDIO_DEVICE_ID_AIC'))
code = """#include <assert.h>
#include <stdbool.h>
#include <errno.h>
#include <stdio.h>
#include "aicbsp_sdio_ids.h"
struct sdio_func { unsigned short vendor, device; int num; };
struct sdio_device_id { int unused; };
static int notifications;
static void *aicbsp_notify_semaphore = &notifications;
static void up(void *p) { ++*(int *)p; }
""" + defines + '\n' + probe + """
int main(void) {
    const unsigned short known[][2] = {
#define ID_ROW(v,d) {v,d},
        AICBSP_SDIO_IDS(ID_ROW)
#undef ID_ROW
    };
    for (unsigned i=0; i<sizeof(known)/sizeof(known[0]); ++i)
        assert(aicbsp_sdio_id_supported(known[i][0], known[i][1]));
    /* Foreign vendors with valid-looking AIC device IDs must not be claimed. */
    for (unsigned i=0; i<sizeof(known)/sizeof(known[0]); ++i) {
        assert(!aicbsp_sdio_id_supported(0x024c, known[i][1]));
        struct sdio_func f={0x024c, known[i][1],1};
        assert(aicbsp_dummy_probe(&f,0)==-ENODEV);
        f.num=2; assert(aicbsp_dummy_probe(&f,0)==-ENODEV);
    }
    struct sdio_func rtl={0x024c,0xb733,1};
    assert(aicbsp_dummy_probe(&rtl,0)==-ENODEV);
    rtl.device=0xb73a; assert(aicbsp_dummy_probe(&rtl,0)==-ENODEV);
    assert(aicbsp_dummy_probe(0,0)==-ENODEV);
    assert(!notifications);
    struct sdio_func primary={0x5449,0x0145,1};
    assert(!aicbsp_dummy_probe(&primary,0)); assert(!notifications);
    struct sdio_func second={0x544a,0x0146,2};
    assert(!aicbsp_dummy_probe(&second,0)); assert(notifications==1);
    struct sdio_func newer={0xc8a1,0x9081,1};
    assert(!aicbsp_dummy_probe(&newer,0)); assert(notifications==2);
    assert(!aicbsp_sdio_id_supported(0x544a,0x0145));
    assert(!aicbsp_sdio_id_supported(0xc8a1,0xb733));
    puts("PASS: exact pairs, foreign IDs, null input, primary reservation and secondary/newer notifications");
}
"""
with tempfile.TemporaryDirectory(prefix='nkos-aic-ids-') as d:
    root=Path(d); (root/'probe.c').write_text(code)
    subprocess.run(['cc','-std=c11','-Wall','-Wextra','-Werror','-Wno-unused-parameter','-I'+str(src),str(root/'probe.c'),'-o',str(root/'probe')],check=True)
    subprocess.run([str(root/'probe')],check=True)
