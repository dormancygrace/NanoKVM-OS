#!/usr/bin/env python3
"""dhcpv6 stop does nothing when no client ran since the boot.

S02ipv6 stops the DHCPv6 client at every apply with IPv6 off, and resolvconf
alone costs about half a second at boot. A client started this boot, or one
an older script started (it left only a pid file), is still cleaned up.
"""
import os
import pathlib
import subprocess
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / 'server/service/network/scripts/nanokvm-dhcpv6'

with tempfile.TemporaryDirectory() as tmp:
    run = pathlib.Path(tmp)
    stubs = run / 'bin'
    stubs.mkdir()
    calls = run / 'calls'
    for name in ('resolvconf', 'ip'):
        stub = stubs / name
        stub.write_text(f'#!/bin/sh\necho "{name} $*" >> {calls}\n')
        stub.chmod(0o755)
    env = dict(os.environ, PATH=f'{stubs}:{os.environ["PATH"]}', NANOKVM_DHCPV6_RUN=str(run))

    def stop():
        subprocess.run(['sh', str(SCRIPT), 'stop', 'eth0'], env=env, check=True)
        return calls.read_text().splitlines() if calls.exists() else []

    assert stop() == [], 'stop ran commands without a client'
    (run / 'udhcpc6.eth0.started').touch()
    assert any(c.startswith('resolvconf -d nkos.dhcpv6.eth0') for c in stop()), 'started client not cleaned up'
    assert not (run / 'udhcpc6.eth0.started').exists(), 'marker kept after stop'
    calls.unlink()
    (run / 'udhcpc6.eth0.pid').write_text('999999\n')
    assert stop(), 'a client from an older script was not cleaned up'
    # A transient resolvconf failure must not turn later stops into no-ops.
    for evidence in ('started', 'pid'):
        calls.unlink(missing_ok=True)
        (run / f'udhcpc6.eth0.{evidence}').write_text('999999\n')
        (stubs / 'resolvconf').write_text(f'#!/bin/sh\necho failed >> {calls}\nexit 1\n')
        stop()
        (stubs / 'resolvconf').write_text(f'#!/bin/sh\necho retried >> {calls}\n')
        stop()
        assert 'retried' in calls.read_text().splitlines(), 'failed DNS cleanup was never retried'
        calls.unlink()
        assert stop() == [], 'successful cleanup did not restore the idle shortcut'
print('PASS: idle stop, started/older client, DNS cleanup retry')
