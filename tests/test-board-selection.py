#!/usr/bin/env python3
"""Exercise the compiled selector with faulty/absent peripheral fixtures."""
from pathlib import Path
import subprocess, tempfile
repo = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as tmp:
    p = Path(tmp)
    (p/'test.c').write_text('''
#define main production_main
#include "nkos-board-probe.c"
#undef main
#include <assert.h>
static int a, b, c, setup_error, setup_calls, probes;
static int pa(void) { return a; }
static int setup(void) { setup_calls++; return setup_error; }
static int pb(unsigned address) { probes++; return address == 0x3d ? b : c; }
static void check(int av, int bv, int cv, int fail, const char *want) {
 a=av; b=bv; c=cv; setup_error=fail; setup_calls=probes=0;
 const char *got=select_profile(pa,setup,pb);
 assert((!got && !want) || (got && want && !strcmp(got,want)));
 // Alpha's reset output shares beta SDA: this must never be touched.
 if (a != 0) assert(setup_calls == 0 && probes == 0);
}
int main(void) {
 check(1,1,1,0,"alpha");
 check(0,1,0,0,"beta");
 check(0,0,1,0,"pcie");
 check(0,0,0,0,"lite");
 check(-1,1,1,0,NULL);
 check(0,0,0,1,NULL);
 check(0,-1,0,0,NULL);
 check(0,0,-1,0,NULL);
 check(0,1,1,0,NULL);
 return 0;
}
''')
    subprocess.run(['gcc','-O2','-Wall','-Wextra','-Werror','-I',str(repo/'firmware/boards'),str(p/'test.c'),'-o',str(p/'test')],check=True)
    subprocess.run([str(p/'test')],check=True)
print('Board selection fault and pin-ownership fixtures passed')
