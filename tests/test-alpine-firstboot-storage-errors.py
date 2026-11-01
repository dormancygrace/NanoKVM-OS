#!/usr/bin/env python3
"""First-boot data partition: every outcome lets the device boot.

Runs firmware/alpine/compat/nanokvm-firstboot-storage without root against a
fake /sys/class/block tree, a fake device directory and stub commands
(sfdisk, partx, mkfs.exfat, mount, mountpoint, logger, sync). Block-device
tests become file tests; the real loop-disk test is
test-alpine-firstboot-storage.py.
"""
from pathlib import Path
import os
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
source = (repo / 'firmware/alpine/compat/nanokvm-firstboot-storage').read_text()

FRESH = {'mmcblk0p1': (1, 131072), 'mmcblk0p2': (131073, 1572864)}
CARD = 4194304  # 2 GiB in sectors


def stub(bin_dir, name, body):
    path = bin_dir / name
    path.write_text('#!/bin/sh\n' + body + '\n')
    path.chmod(0o755)


def run_case(partitions=FRESH, card=CARD, marker=True, sfdisk_rc=0, mkfs_rc=0,
             mount_rc=0, p3=None, runs=1, between=None, created_start=1705984,
             rm_rc=0, sync_rc=0, p3_appears=True):
    with tempfile.TemporaryDirectory(prefix='nkos-firstboot-') as temp:
        t = Path(temp)
        sys_block, dev, data, bin_dir = t / "sysfs", t / "blockdev", t / "data", t / "bin"
        for d in (sys_block, dev, bin_dir):
            d.mkdir()
        (sys_block / 'mmcblk0').mkdir()
        (sys_block / 'mmcblk0/size').write_text(f'{card}\n')
        layout = dict(partitions)
        if p3 is not None:
            layout['mmcblk0p3'] = p3
        for name, (start, size) in layout.items():
            (sys_block / name).mkdir()
            (sys_block / name / 'start').write_text(f'{start}\n')
            (sys_block / name / 'size').write_text(f'{size}\n')
            (dev / name).touch()
        marker_path = t / 'marker'
        if marker:
            marker_path.touch()
        calls = t / 'calls'
        calls.touch()
        log = f'echo "$(basename "$0") $*" >> {calls}'
        stub(bin_dir, 'sfdisk', f'{log}\ncat >/dev/null\n'
             f'[ "${{SFDISK_RC:-0}}" = 0 ] || exit "$SFDISK_RC"\n'
             f'mkdir -p {sys_block}/mmcblk0p3\necho {created_start} > {sys_block}/mmcblk0p3/start\n'
             f'echo $(({card} - {created_start})) > {sys_block}/mmcblk0p3/size\n'
             + (f'touch {dev}/mmcblk0p3' if p3_appears else ':'))
        stub(bin_dir, 'partx', log)
        stub(bin_dir, 'mkfs.exfat', f'{log}\nexit "${{MKFS_RC:-0}}"')
        stub(bin_dir, 'mount', f'{log}\nexit "${{MOUNT_RC:-0}}"')
        stub(bin_dir, 'mountpoint', 'exit 1')
        stub(bin_dir, 'logger', log)
        stub(bin_dir, 'rm', f'{log}\n[ "${{RM_RC:-0}}" = 0 ] || exit "$RM_RC"\nexec /bin/rm "$@"')
        stub(bin_dir, 'sync', 'exit "${SYNC_RC:-0}"')
        stub(bin_dir, 'sleep', 'exit 0')
        script = (source
                  .replace('export PATH=/usr/sbin:/usr/bin:/sbin:/bin', f'export PATH={bin_dir}:$PATH')
                  .replace('/etc/nanokvm-firstboot-storage', str(marker_path))
                  .replace('/var/log/nanokvm-firstboot-storage.log', str(t / 'reasons.log'))
                  .replace('/sys/class/block/', f'{sys_block}/')
                  .replace('/dev/mmcblk0', f'{dev}/mmcblk0')
                  .replace('/data', str(data))
                  .replace('[ -b ', '[ -e ').replace('! -b ', '! -e '))
        assert '/dev/mmcblk0' not in script and '-b ' not in script
        script_path = t / 'firstboot.sh'
        script_path.write_text(script)
        results = []
        for index in range(runs):
            env = dict(os.environ, SFDISK_RC=str(sfdisk_rc), MKFS_RC=str(mkfs_rc), MOUNT_RC=str(mount_rc), RM_RC=str(rm_rc), SYNC_RC=str(sync_rc))
            if between and index > 0:
                env.update(between)
            result = subprocess.run(['sh', str(script_path)], text=True, capture_output=True, env=env)
            results.append((result.returncode, result.stderr))
        reasons = (t / 'reasons.log').read_text() if (t / 'reasons.log').exists() else ''
        return results, marker_path.exists(), calls.read_text() + reasons


def check(name, condition, detail=''):
    assert condition, f'{name}: {detail}'
    print('ok', name)


# Fresh image: partition 3 is created and formatted once.
results, marker, calls = run_case()
check('fresh card', results == [(0, '')] and not marker and 'sfdisk' in calls and 'mkfs.exfat' in calls, (results, calls))

# Aligned images and foreign/mixed geometry; preserve the legacy path.
ALIGNED = {'mmcblk0p1': (8192, 131072), 'mmcblk0p2': (139264, 1572864)}
results, marker, calls = run_case(partitions=ALIGNED, card=CARD, created_start=1712128)
check('aligned card', results == [(0, '')] and not marker and 'mkfs.exfat' in calls, (results, calls))
results, marker, calls = run_case(partitions=ALIGNED, p3=(1705984, 100000))
check('aligned card with legacy p3 is not formatted', results[0][0] == 0 and not marker and 'mkfs.exfat' not in calls, (results, calls))
results, marker, calls = run_case(partitions={'mmcblk0p1': (1, 131072), 'mmcblk0p2': (139264, 1572864)})
check('mixed boot/root layout is not formatted', results[0][0] == 0 and not marker and 'sfdisk' not in calls and 'mkfs.exfat' not in calls, (results, calls))
results, marker, calls = run_case(partitions=ALIGNED, card=1712128 + 196607)
check('less than 96 MiB data refused', results[0][0] == 0 and not marker and 'sfdisk' not in calls, (results, calls))
results, marker, calls = run_case(partitions=ALIGNED, card=1712128 + 196608, created_start=1712128)
check('96 MiB data accepted', results == [(0, '')] and not marker and 'mkfs.exfat' in calls, (results, calls))

# Decimal 1 GB cards work with both supported fresh layouts.
for layout, start in [(FRESH, 1705984), (ALIGNED, 1712128)]:
    results, marker, calls = run_case(partitions=layout, card=1_000_000_000 // 512,
                                     created_start=start, runs=2)
    check(f'1 GB fresh card at {start}', results == [(0, ''), (0, '')] and not marker
          and calls.count('sfdisk') == 1 and calls.count('mkfs.exfat') == 1, (results, calls))
    # An installed device has no fresh-image marker, even with a tiny /data.
    results, marker, calls = run_case(partitions=layout, card=1_000_000_000 // 512,
                                     p3=(start, 65536), marker=False)
    check(f'existing small data at {start} untouched', results == [(0, '')]
          and not marker and calls == '', (results, calls))

# No marker: nothing to do.
results, marker, calls = run_case(marker=False)
check('no marker', results[0][0] == 0 and calls == '', calls)

# Not a fresh image: never format, drop the marker, let the boot go on.
results, marker, calls = run_case(partitions={'mmcblk0p1': (1, 131072), 'mmcblk0p2': (131073, 999999)})
check('other layout', results[0][0] == 0 and not marker and 'mkfs' not in calls and 'sfdisk' not in calls, (results, calls))
results, marker, calls = run_case(partitions={'mmcblk0p1': (1, 131072)})
check('missing partition 2', results[0][0] == 0 and not marker and 'mkfs' not in calls, (results, calls))
results, marker, calls = run_case(p3=(2000000, 100000))
check('foreign partition 3', results[0][0] == 0 and not marker and 'mkfs' not in calls, (results, calls))
results, marker, calls = run_case(card=1705984 + 1000)
check('card too small', results[0][0] == 0 and not marker and 'sfdisk' not in calls and 'mkfs' not in calls, (results, calls))

# The reason of a skipped card is kept in a file: syslog may not run yet.
results, marker, calls = run_case(card=1705984 + 1000)
check('reason recorded', 'too small' in calls, calls)

# Transient failures keep the marker for the next boot and report status 1.
results, marker, calls = run_case(sfdisk_rc=1)
check('partitioning fails', results[0][0] == 1 and marker and 'mkfs' not in calls, (results, calls))
results, marker, calls = run_case(p3_appears=False)
check('partition device never appears', results[0][0] == 1 and marker and 'mkfs' not in calls, (results, calls))
results, marker, calls = run_case(mkfs_rc=1, runs=2, between={'MKFS_RC': '0'})
check('format fails, then succeeds at the next boot',
      [r[0] for r in results] == [1, 0] and not marker and calls.count('mkfs.exfat') == 2, (results, calls))

# A p3 created here that reads back elsewhere is a retry, never a foreign layout.
results, marker, calls = run_case(created_start=1705985)
check('new partition reads back wrong', results[0][0] == 1 and marker and 'mkfs.exfat' not in calls, (results, calls))

# A mount failure after formatting must not lead to a second format.
results, marker, calls = run_case(mount_rc=1, runs=2)
check('mount fails after format', [r[0] for r in results] == [0, 0] and not marker and calls.count('mkfs.exfat') == 1, (results, calls))

# A failed marker retirement must not expose a filesystem scheduled for mkfs.
results, marker, calls = run_case(rm_rc=1)
check('marker removal failure keeps data unavailable',
      results[0][0] == 1 and marker and 'mount ' not in calls, (results, calls))
results, marker, calls = run_case(p3=(1705984, CARD - 1705984), sync_rc=1)
check('marker sync failure keeps data unavailable',
      results[0][0] == 1 and 'mount ' not in calls, (results, calls))


print('PASS first-boot storage: safe layouts, retries, one-shot format and boot continues')
