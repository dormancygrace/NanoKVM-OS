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
    write(server / 'dl_lib/libsys.so')
    write(web / 'index.html', b'<!doctype html>\n')
    devmem = work / 'devmem' / 'devmem'
    write(devmem, b'devmem applet\n')
    devmem.chmod(0o755)
    write(devmem.parent / 'busybox-LICENSE', b'BusyBox GPL-2.0-only\n')

    subprocess.run([
        str(root / 'scripts/prepare-alpine-release-payloads.py'),
        '--port-payloads', str(port),
        '--accepted-root', str(accepted),
        '--server', str(server),
        '--web', str(web),
        '--devmem', str(devmem),
        '--output', str(output),
    ], check=True)

    expected = enhanced.read_bytes()
    base_s15 = output / 'base/usr/libexec/nanokvm/legacy/S15kvmhwd'
    app_s15 = output / 'app/kvmapp/system/init.d/S15kvmhwd'
    assert base_s15.read_bytes() == expected
    assert app_s15.read_bytes() == expected
    assert (output / 'base/etc/init.d/S15kvmhwd').readlink() == Path(
        '/usr/libexec/nanokvm/legacy/S15kvmhwd')
    assert (output / 'base/usr/sbin/devmem').read_bytes() == devmem.read_bytes()
    assert (output / 'base/usr/sbin/devmem').stat().st_mode & 0o111
    assert (output / 'base/usr/share/licenses/nanokvm-base/busybox-LICENSE').is_file()

print('Enhanced S15 packaging test passed')
