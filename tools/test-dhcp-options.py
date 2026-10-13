#!/usr/bin/env python3
"""The shipped S30eth and S30wifi start udhcpc with -B and -O 121 (classless routes).

Alpine's udhcpc script applies the routes. The server's DNS and gateway hooks
are tested in server/service/network/udhcpc_hooks_test.go.
"""
from pathlib import Path

root=Path(__file__).resolve().parents[1]
for name in ('S30eth','S30wifi'):
    script_lines=(root/'kvmapp/system/init.d'/name).read_text().splitlines()
    calls=[i for i,line in enumerate(script_lines)
           if ('udhcpc ' in line or '"$NANOKVM_UDHCPC" ' in line) and not line.lstrip().startswith('#')]
    assert calls,name
    for i in calls:
        line=script_lines[i]
        if '"$@"' in line:
            # The options are assembled with `set --` just before the call;
            # later `set -- "$@" ...` lines only append the hostname.
            starts=[l for l in script_lines[:i] if l.lstrip().startswith('set -- ') and not l.lstrip().startswith('set -- "$@"')]
            assert starts,(name,line)
            line=starts[-1]
        assert '-B' in line and '-O 121' in line, line
print('DHCP: S30eth and S30wifi start udhcpc with -B -O 121')
