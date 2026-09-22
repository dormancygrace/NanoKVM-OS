#!/usr/bin/env python3
import subprocess
import tempfile
from pathlib import Path


root = Path(__file__).resolve().parents[1]
enhanced = root / 'firmware/buildroot/board/enhanced/init.d/S15kvmhwd'


def write(path: Path, data: bytes = b'x\n') -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


with tempfile.TemporaryDirectory(prefix='nanokvm-s15-package-') as temporary:
    work = Path(temporary)
    port = work / 'port'
    accepted = work / 'accepted'
    server = work / 'server'
    web = work / 'web'
    output = work / 'output'

    for directory in ('base', 'app', 'firmware-sg2002', 'release'):
        (port / directory).mkdir(parents=True)
    write(port / 'base/usr/libexec/nanokvm/legacy/S15kvmhwd', b'vendor base\n')
    write(port / 'app/kvmapp/system/init.d/S15kvmhwd', b'vendor app\n')
    write(port / 'app/kvmapp/server/web/old.html')

    for name in ('nkos-board-probe', 'nanokvm_update_edid',
                 'nanokvm-wifi-tx-live', 'nanokvm-wifi-tx-policy'):
        write(accepted / 'usr/sbin' / name)
    for name in ('nanokvm-buildroot', 'chrony.conf', 'console_handler.sh'):
        write(accepted / 'etc' / name)
    write(accepted / 'mnt/data/sensor_cfg.ini.LT')

    for name in ('nkos-update', 'nkos-apply-updates', 'NanoKVM-Server.stripped'):
        write(server / name)
    write(web / 'index.html', b'<!doctype html>\n')

    subprocess.run([
        str(root / 'scripts/prepare-alpine-release-payloads.py'),
        '--port-payloads', str(port),
        '--accepted-root', str(accepted),
        '--server', str(server),
        '--web', str(web),
        '--output', str(output),
    ], check=True)

    expected = enhanced.read_bytes()
    base_s15 = output / 'base/usr/libexec/nanokvm/legacy/S15kvmhwd'
    app_s15 = output / 'app/kvmapp/system/init.d/S15kvmhwd'
    assert base_s15.read_bytes() == expected
    assert app_s15.read_bytes() == expected
    assert (output / 'base/etc/init.d/S15kvmhwd').readlink() == Path(
        '/usr/libexec/nanokvm/legacy/S15kvmhwd')

print('Enhanced S15 packaging test passed')
