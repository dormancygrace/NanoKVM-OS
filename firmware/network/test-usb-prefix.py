#!/usr/bin/env python3
"""Exercise the actual S30rndis prefix function without invoking network actions."""
import argparse, shlex, subprocess
from pathlib import Path
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--script',type=Path,required=True)
p.add_argument('--emit-shell',type=Path)
a=p.parse_args()
s=a.script.read_text()
function=s[s.index('prefix() {'):s.index('\nstart() {')]
cases=[('normal','10.200.201\n','10.200.201'),
       ('Windows CRLF','10.200.201\r\n','10.200.201'),
       ('multiple prefixes','10.200.201\n10.201.202\n',None),
       ('empty','',None),('whitespace','10.200.201 \n',None),
       ('too large','10.200.999\n',None),('leading zero','10.020.201\n',None),
       ('loopback','127.0.0\n',None),('multicast','224.0.0\n',None)]
shell='''#!/bin/sh
set -eu
prefix_test=$(mktemp -d /tmp/nanokvm-prefix-check.XXXXXX)
trap 'rm -rf -- "$prefix_test"' EXIT
NANOKVM_USB_NET_BOOT=$prefix_test
'''+function+'\n'
for name,value,expected in cases:
    shell+='printf %s '+shlex.quote(value)+' > "$prefix_test/rndis.ipv4_prefix"\n'
    if expected is not None:
        shell+='result=$(prefix)\n[ "$result" = '+shlex.quote(expected)+' ]\n'
    else:
        shell+='if prefix >/dev/null 2>&1; then echo '+shlex.quote('FAIL '+name)+'; exit 1; fi\n'
    shell+='echo '+shlex.quote('PASS '+name)+'\n'
shell+="echo 'PASS nine prefix cases; no network service invoked'\n"
if a.emit_shell:
    a.emit_shell.write_text(shell)
else:
    subprocess.run(['sh'],input=shell,text=True,check=True)
