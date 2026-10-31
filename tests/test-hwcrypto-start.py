#!/usr/bin/env python3
"""S94hwcrypto loads the CryptoDMA module once: never beside a loaded one,
including the module from before 3.7.0 (sg2002_aes_probe) until the reboot."""
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = (ROOT / 'kvmapp/system/init.d/S94hwcrypto').read_text()

with tempfile.TemporaryDirectory() as directory:
    work = Path(directory)
    root = work / 'root'
    bindir = work / 'bin'
    bindir.mkdir()
    log = work / 'modprobe.log'
    (bindir / 'modprobe').write_text(f'#!/bin/sh\necho "$*" >> {log}\n')
    (bindir / 'uname').write_text('#!/bin/sh\necho 7.3.1-test\n')
    for tool in bindir.iterdir():
        tool.chmod(0o755)
    # Only the test copy reads the temporary root.
    script = SCRIPT
    for path in ('/sys/', '/lib/', '/etc/'):
        script = script.replace(path, str(root) + path)
    (root / 'lib/modules/7.3.1-test/extra').mkdir(parents=True)
    (root / 'lib/modules/7.3.1-test/extra/nanokvm_hwcrypto.ko').write_bytes(b'')

    def start(loaded=(), disabled=False):
        log.unlink(missing_ok=True)
        for name in ('nanokvm_hwcrypto', 'sg2002_aes_probe'):
            module = root / 'sys/module' / name
            if name in loaded:
                module.mkdir(parents=True, exist_ok=True)
            elif module.exists():
                module.rmdir()
        flag = root / 'etc/kvm/disable_hardware_aes'
        flag.parent.mkdir(parents=True, exist_ok=True)
        if disabled:
            flag.write_text('')
        else:
            flag.unlink(missing_ok=True)
        subprocess.run(['sh', '-s', 'start'], input=script, text=True, check=True,
                       env={'PATH': f'{bindir}:/usr/bin:/bin'})
        return log.read_text().split() if log.exists() else []

    assert start() == ['nanokvm_hwcrypto'], 'the module loads'
    assert start(loaded={'nanokvm_hwcrypto'}) == [], 'already loaded'
    assert start(loaded={'sg2002_aes_probe'}) == [], 'old module loaded: no second driver'
    assert start(disabled=True) == [], 'hardware AES off'
print('PASS: S94hwcrypto loads nanokvm_hwcrypto once, never beside it or sg2002_aes_probe, not when disabled')
