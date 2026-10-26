#!/bin/sh
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
exec python3 - "$repo" <<'PY'
import os, pathlib, subprocess, sys, tempfile
R=pathlib.Path(sys.argv[1])
helper=R/'firmware/alpine/compat/nanokvm-ethernet-rx-batching'
checks=0
with tempfile.TemporaryDirectory(prefix='nanokvm-rx-test-') as temp:
    root=pathlib.Path(temp)
    net=root/'net';eth=net/'eth0'
    (eth/'device/of_node').mkdir(parents=True)
    compat=eth/'device/of_node/compatible'
    flush=eth/'gro_flush_timeout';defer=eth/'napi_defer_hard_irqs'
    bins=root/'bin';bins.mkdir()
    env=dict(os.environ,NANOKVM_RX_NET_CLASS_DIR=str(net),NANOKVM_ETH_RX_BATCHING="1")
    def reset():
        compat.write_bytes(b'sophgo,cv1800b-dwmac\0snps,dwmac-3.70a\0')
        for p in (flush,defer):
            if p.is_dir():p.rmdir()
            p.write_text('0\n')
    def run(**overrides):
        return subprocess.run(['sh',str(helper)],env=env|overrides,text=True,capture_output=True)
    def values():return (flush.read_text().strip(),defer.read_text().strip())
    reset()
    defaults=env.copy();defaults.pop('NANOKVM_ETH_RX_BATCHING')
    assert subprocess.run(['sh',str(helper)],env=defaults).returncode==0
    assert values()==('0','0')
    checks+=1
    assert run().returncode==0
    assert values()==('200000','2')
    before=(flush.stat().st_mtime_ns,defer.stat().st_mtime_ns)
    assert run().returncode==0
    assert before==(flush.stat().st_mtime_ns,defer.stat().st_mtime_ns)
    checks+=2
    for a,b in [('500000','4'),('200000','0'),('0','1')]:
        reset();flush.write_text(a);defer.write_text(b)
        assert run().returncode==0 and values()==(a,b)
        checks+=1
    reset()
    assert run(NANOKVM_ETH_RX_BATCHING='0').returncode==0 and values()==('0','0')
    checks+=1
    for compatible in [b'other,mac\0',b'sophgo,cv1800b-dwmac-extra\0']:
        reset();compat.write_bytes(compatible)
        assert run().returncode==0 and values()==('0','0')
        checks+=1
    reset();compat.unlink()
    assert run().returncode==0 and values()==('0','0');checks+=1
    reset();defer.unlink()
    assert run().returncode==0 and flush.read_text()=='0\n';checks+=1
    reset()
    assert run(NANOKVM_RX_NET_CLASS_DIR=str(root/'missing')).returncode==0
    checks+=1

    # An actual redirection failure after a successful read, even as CI root.
    fakecat=bins/'cat'
    fakecat.write_text('#!/bin/sh\n/bin/cat "$@"\nif [ "$1" = "$FAIL_PATH" ] && [ ! -d "$1" ]; then\n  rm -- "$1"\n  mkdir -- "$1"\nfi\n')
    fakecat.chmod(0o755)
    for failed in (flush,defer):
        reset()
        p=run(PATH=str(bins)+':'+os.environ['PATH'],FAIL_PATH=str(failed))
        assert p.returncode!=0 and 'Could not apply' in p.stderr
        other=defer if failed==flush else flush
        assert other.read_text().strip()=='0',p.stderr
        checks+=1
    # A kernel accepting the write but reporting a different setting rolls back.
    fakecat.write_text('#!/bin/sh\nv=$(/bin/cat "$@") || exit 1\nif [ "$1" = "$FAIL_PATH" ] && [ "$v" = 200000 ]; then printf "1\\n"; else printf "%s\\n" "$v"; fi\n')
    reset()
    p=run(PATH=str(bins)+':'+os.environ['PATH'],FAIL_PATH=str(flush))
    assert p.returncode!=0 and values()==('0','0')
    checks+=1
    fakecat.unlink()

    # Actual startup entrypoint, with private IP/DHCP stand-ins.
    calls=root/'calls'
    for command in ['ip','udhcpc','arping','modprobe']:
        p=bins/command
        p.write_text('#!/bin/sh\nprintf "%s %s\\n" "${0##*/}" "$*" >> "$TEST_CALLS"\n')
        p.chmod(0o755)
    failhelper=bins/'fail-helper'
    failhelper.write_text('#!/bin/sh\nexit 1\n');failhelper.chmod(0o755)
    networkenv=env|{
        'TEST_CALLS':str(calls),
        'NANOKVM_ETH_RX_TUNER':str(helper),
        'NANOKVM_HOSTNAME_FILE':str(root/'hostname'),
        'NANOKVM_ETH_CONFIG':str(root/'address'),
        'NANOKVM_ETH_DISABLED':str(root/'disabled'),
        'NANOKVM_ETH_DHCP_PID':str(root/'pid'),
        'NANOKVM_ETH_VLAN':str(root/'vlan'),
        'NANOKVM_ETH_VLAN_STATE':str(root/'vlan-state'),
        'NANOKVM_IPV6_INIT':str(root/'no-ipv6-script'),
        'NANOKVM_IP':str(bins/'ip'),
        'NANOKVM_UDHCPC':str(bins/'udhcpc'),
        'NANOKVM_ARPING':str(bins/'arping'),
        'NANOKVM_MODPROBE':str(bins/'modprobe')}
    def start(**overrides):
        calls.write_text('')
        return subprocess.run(['sh',str(R/'kvmapp/system/init.d/S30eth'),'start'],
                              env=networkenv|overrides,text=True,capture_output=True)
    reset()
    p=start(NANOKVM_ETH_RX_BATCHING='',NANOKVM_ETH_RX_TUNER=str(failhelper))
    assert p.returncode==0 and values()==('0','0') and 'unavailable' not in p.stderr
    assert 'udhcpc ' in calls.read_text()
    checks+=1
    p=start()
    assert p.returncode==0 and values()==('200000','2'),p.stderr
    assert 'ip link set dev eth0 up' in calls.read_text()
    assert 'udhcpc ' in calls.read_text()
    checks+=1
    reset();(root/'disabled').touch()
    assert start().returncode==0 and values()==('0','0')
    assert 'ip link set dev eth0 up' not in calls.read_text()
    assert 'udhcpc ' not in calls.read_text()
    (root/'disabled').unlink();checks+=1
    reset();(root/'vlan').write_text('100\n')
    vlan=net/'eth0.100';vlan.mkdir()
    for name in ('gro_flush_timeout','napi_defer_hard_irqs'):
        (vlan/name).write_text('0\n')
    assert start().returncode==0 and values()==('200000','2')
    assert 'ip link set dev eth0.100 up' in calls.read_text()
    assert all((vlan/name).read_text()=='0\n' for name in ('gro_flush_timeout','napi_defer_hard_irqs'))
    (root/'vlan').unlink();checks+=1
    reset()
    p=start(NANOKVM_ETH_RX_TUNER=str(failhelper))
    assert p.returncode==0 and 'continuing network setup' in p.stderr
    assert 'udhcpc ' in calls.read_text() and values()==('0','0')
    checks+=1
    reset()
    assert start(NANOKVM_ETH_RX_TUNER=str(root/'missing-helper')).returncode==0
    assert 'udhcpc ' in calls.read_text() and values()==('0','0')
    checks+=1

manifest=(R/'platform/packages.list').read_text().splitlines()
assert any(line.split()==['base','file','/usr/libexec/nanokvm/ethernet-rx-batching','0755',
    'firmware/alpine/compat/nanokvm-ethernet-rx-batching'] for line in manifest)
checks+=1
print(f'PASS: {checks} Ethernet receive policy, failure, startup and packaging checks')
PY
