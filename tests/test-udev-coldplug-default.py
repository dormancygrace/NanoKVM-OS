#!/usr/bin/env python3
"""Execute the package-hook default and shell configuration with user overrides."""
from pathlib import Path
import os
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
shells = [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else [])
cases = [
    ('#rc_coldplug="YES"\n', {}, 'NO'),
    ('# no final newline', {}, 'NO'),
    ('rc_coldplug="YES"\n', {}, 'YES'),
    ('export rc_coldplug="YES"\n', {}, 'YES'),
    ('readonly rc_coldplug="YES"\n', {}, 'YES'),
    ('rc_coldplug="NO"\n', {}, 'NO'),
    ('rc_coldplug="yes"', {}, 'yes'),
    ('# service defaults\n', {'rc_coldplug': 'YES'}, 'YES'),
    ('# service defaults\n', {'RC_COLDPLUG': 'YES'}, 'YES'),
    ('rc_coldplug="NO"\n', {'RC_COLDPLUG': 'YES'}, 'NO'),
    ('export rc_coldplug=""\n', {'RC_COLDPLUG': 'YES'}, 'YES'),
]
for hook in ('post-install', 'post-upgrade'):
    source = (root / 'firmware/alpine/packages/nanokvm-base' /
              ('nanokvm-base.' + hook)).read_text()
    begin = source.index('# NanoKVM udev coldplug default:')
    end = source.index('# End NanoKVM udev coldplug default', begin)
    block = source[begin:end]
    for shell in shells:
        for initial, inherited, want in cases:
            with tempfile.TemporaryDirectory() as directory:
                config = Path(directory) / 'udev-trigger'
                config.write_text(initial)
                env = dict(os.environ)
                env.pop('rc_coldplug', None)
                env.pop('RC_COLDPLUG', None)
                env.update(inherited)
                script = 'set -eu\n' + block.replace('/etc/conf.d/udev-trigger', str(config))
                subprocess.run(shell, input=script, text=True, env=env, check=True)
                once = config.read_bytes()
                subprocess.run(shell, input=script, text=True, env=env, check=True)
                assert config.read_bytes() == once, 'duplicate default'
                assert config.read_text().startswith(initial), 'user text changed'
                got = subprocess.check_output(shell + ['-c',
                    '. "$1"; printf "%s" "$rc_coldplug"', 'sh', str(config)], env=env, text=True)
                assert got == want, (hook, shell, initial, inherited, got)
        with tempfile.TemporaryDirectory() as directory:
            absent = Path(directory) / 'missing'
            subprocess.run(shell, input='set -eu\n' + block.replace('/etc/conf.d/udev-trigger', str(absent)),
                           text=True, check=True)
            assert not absent.exists(), 'created absent service config'
        print('PASS coldplug fallback, overrides, idempotence, absent config:', hook, shell)
