#!/usr/bin/env python3
"""Exercise the real initramfs User Key decision without accessing hardware."""
import argparse
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--shell', action='append', type=shlex.split, default=[],
                    help='additional shell command, e.g. a target BusyBox under QEMU')
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
source = (root / 'platform/boot/init').read_text()
start = source.index('user_key_down() {\n')
end = source.index('\numount /boot\n', start)
flow = source[start:end]
shells = [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else []) + args.shell
cases = [
    ('released', [(0, '0xFFFFFFFF')], ['read']),
    ('only key bit high', [(0, '0x40000000')], ['read']),
    ('held', [(0, '0xBFFFFFFF'), (0, '0xBFFFFFFF')], ['read', 'sleep', 'read', 'msc']),
    ('all bits low', [(0, '0x00000000'), (0, '0x00000000')], ['read', 'sleep', 'read', 'msc']),
    ('released during wait', [(0, '0xBFFFFFFF'), (0, '0xFFFFFFFF')], ['read', 'sleep', 'read']),
    ('read fails initially', [(1, '')], ['read']),
    ('read fails after wait', [(0, '0x00000000'), (1, '')], ['read', 'sleep', 'read']),
    ('missing applet', [(127, '')], ['read']),
    ('unrecognized response', [(0, 'unreadable')], ['read']),
]
with tempfile.TemporaryDirectory(prefix='nkos-user-key-') as temporary:
    directory = Path(temporary)
    # Keep the documented stock patch an exact reconstruction of /init.
    reconstructed = directory / 'init'
    subprocess.run(['patch', '-s', '-o', str(reconstructed),
                    str(root / 'platform/boot/stock-init'),
                    str(root / 'platform/boot/stock-init.diff')], check=True)
    assert reconstructed.read_text() == source, 'stock-init.diff is stale'
    assert 'slink /devmem busybox 777 0 0' in (root / 'platform/boot/initramfs.list').read_text()
    assert 'CONFIG_DEVMEM=y' in (root / 'firmware/buildroot/board/busybox-initramfs.config').read_text().splitlines()
    assert 'CONFIG_DEVMEM=y' in (root / 'platform/kernel/config').read_text().splitlines()
    for shell in shells:
        for name, reads, expected in cases:
            events = directory / 'events'
            counter = directory / 'counter'
            events.write_text('')
            counter.write_text('0\n')
            responses = '\n'.join(f'{i}) printf "%s\\n" {shlex.quote(value)}; return {status} ;;'
                                  for i, (status, value) in enumerate(reads, 1))
            fixture = f'''
EVENTS={shlex.quote(str(events))}
COUNTER={shlex.quote(str(counter))}
devmem() {{
    [ "$#" = 2 ] && [ "$1" = 0x03020050 ] && [ "$2" = 32 ] || return 99
    printf 'read\\n' >> "$EVENTS"
    read -r count < "$COUNTER"
    count=$((count + 1))
    printf '%s\\n' "$count" > "$COUNTER"
    case "$count" in
{responses}
    *) return 98 ;;
    esac
}}
sleep() {{
    [ "$#" = 1 ] && [ "$1" = 0.5 ] || return 99
    printf 'sleep\\n' >> "$EVENTS"
}}
msc() {{ printf 'msc\\n' >> "$EVENTS"; }}
{flow}
printf 'boot continues\\n'
'''
            result = subprocess.run(shell, input=fixture, text=True, capture_output=True, timeout=10)
            assert result.returncode == 0, (shell, name, result.stdout, result.stderr)
            assert result.stdout == 'boot continues\n' and not result.stderr, (shell, name, result)
            assert events.read_text().splitlines() == expected, (shell, name, events.read_text(), expected)
        print('PASS: held/released key, debounce, read failures and continued boot:', shell)
