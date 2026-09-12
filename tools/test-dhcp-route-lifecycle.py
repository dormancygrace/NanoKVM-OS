#!/usr/bin/env python3
"""Run the real DHCP hook against an isolated Linux network namespace (root)."""
import json, os, subprocess, tempfile, uuid
from pathlib import Path
root=Path(__file__).resolve().parents[1]
name='nkos-dhcp-'+uuid.uuid4().hex[:10]
def run(*args, **kw):
    return subprocess.run(args,check=True,text=True,capture_output=True,**kw).stdout
def ip(*args): return run('ip','netns','exec',name,'ip',*args)
try:
    run('ip','netns','add',name)
    ip('link','add','test0','type','dummy');ip('link','set','test0','up')
    ip('addr','add','192.0.2.20/24','dev','test0')
    ip('route','add','203.0.113.0/24','via','192.0.2.9','dev','test0','proto','static')
    ip('link','add','vpn0','type','dummy');ip('link','set','vpn0','up')
    ip('addr','add','10.7.1.28/24','dev','vpn0')
    ip('route','add','10.42.0.0/16','dev','vpn0','proto','static')
    with tempfile.TemporaryDirectory(prefix='nkos-dhcp-hook-') as td:
        td=Path(td);resolver=td/'resolv';resolver.touch()
        script=(root/'firmware/buildroot/board/overlay/usr/share/udhcpc/default.script').read_text()
        script=script.replace('RESOLV_CONF="/etc/resolv.conf"','RESOLV_CONF="'+str(resolver)+'"')
        script=script.replace('[ -e /boot/resolv.conf ] || command -v resolvconf >/dev/null 2>&1','false')
        script=script.replace('/usr/sbin/avahi-autoipd',str(td/'absent-avahi'))
        # Address management is unchanged; preserve the real namespace addresses
        # without requiring the legacy net-tools executable on the build host.
        script=script.replace('/sbin/ifconfig','/bin/true')
        hook=td/'hook';hook.write_text(script)
        env=dict(os.environ,interface='test0',ip='192.0.2.20',subnet='255.255.255.0',dns='',domain='',search='',ipv6='')
        def lease(action,static='',router=''):
            run('ip','netns','exec',name,'sh',str(hook),action,env=dict(env,staticroutes=static,router=router))
        def routes(): return json.loads(ip('-j','-4','route','show'))
        def own(): return [r for r in routes() if r.get('protocol')=='dhcp']
        def preserved():
            r=routes()
            assert any(x.get('dst')=='203.0.113.0/24' and x.get('protocol')=='static' for x in r),r
            assert any(x.get('dst')=='10.42.0.0/16' and x.get('dev')=='vpn0' for x in r),r
            assert any(x.get('dst')=='192.0.2.0/24' and x.get('protocol')=='kernel' for x in r),r
        lease('bound','198.51.100.0/24 192.0.2.2 0.0.0.0/0 192.0.2.3 10.100.0.0/16 0.0.0.0','192.0.2.1')
        assert len(own())==3,own()
        assert any(r.get('dst')=='default' and r.get('gateway')=='192.0.2.3' for r in own()),own()
        assert any(r.get('dst')=='10.100.0.0/16' and 'gateway' not in r for r in own()),own()
        preserved()
        lease('renew',router='192.0.2.1 192.0.2.4')
        assert len(own())==2 and all(r.get('dst')=='default' for r in own()),own()
        assert {r.get('metric') for r in own()}=={100,101},own();preserved()
        lease('renew')
        assert not own(),own();preserved()
        lease('bound','198.51.100.0/24 192.0.2.2')
        lease('deconfig');assert not own(),own();preserved()
        # Route ownership must not replace an existing static route even when
        # prefix, interface and priority happen to match.
        ip('route','add','198.51.100.0/24','via','192.0.2.9','dev','test0','proto','static','metric','100')
        lease('bound','198.51.100.0/24 192.0.2.2')
        assert not own(),own()
        assert any(r.get('dst')=='198.51.100.0/24' and r.get('gateway')=='192.0.2.9' for r in routes())
        preserved()
    print('PASS: option121 precedence, on-link route, multiple gateways, renew withdrawals, deconfig, static/VPN/connected preservation and collision safety')
finally:
    subprocess.run(['ip','netns','del',name],check=False,capture_output=True)
