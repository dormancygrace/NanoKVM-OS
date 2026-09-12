"""Exercise startup policy with command doubles; never mount or format a disk."""
from pathlib import Path
import os, subprocess, tempfile, json

script = Path(__file__).resolve().parents[1] / 'firmware/buildroot/board/enhanced/init.d/S01fs'
results = []
with tempfile.TemporaryDirectory(prefix='enhanced-fs-') as directory:
    root = Path(directory)
    bindir = root / 'bin'
    bindir.mkdir()
    mock = '''#!/bin/sh
name=${0##*/}
printf '%s %s\\n' "$name" "$*" >> "$TEST_LOG"
case "$name" in
mountpoint)
  [ "$TEST_CASE" = mounted ] && exit 0
  exit 1;;
mount)
  [ "$TEST_CASE" = boot-failure ] && exit 1
  [ "$TEST_CASE" = data-failure ] && [ "$1" = /dev/loop0 ] && exit 1
  exit 0;;
blkid)
  [ "$TEST_CASE" = unknown ] || printf 'TYPE="exfat"\\n'
  exit 0;;
parted|mkfs.exfat) exit 99;;
esac
'''
    for name in ['mountpoint', 'mount', 'blkid', 'parted', 'mkfs.exfat']:
        path = bindir / name
        path.write_text(mock)
        path.chmod(0o755)
    # Only stat(2) sees this existing block device. All access commands above
    # are stubs, so no host device is opened.
    assert Path('/dev/loop0').is_block_device()
    for case in ['missing', 'unknown', 'recognized', 'data-failure', 'boot-failure', 'mounted']:
        log, marker = root / 'calls', root / 'ready'
        log.write_text('')
        marker.write_text('stale')
        env = dict(os.environ, PATH=str(bindir) + ':/usr/bin:/bin', TEST_CASE=case,
                   TEST_LOG=str(log), NANOKVM_BOOT_DIR=str(root / 'boot'),
                   NANOKVM_DATA_DIR=str(root / 'data'),
                   NANOKVM_BOOT_PART='/unused-boot-device',
                   NANOKVM_DATA_PART=str(root / 'absent') if case == 'missing' else '/dev/loop0',
                   NANOKVM_DISK0_MARKER=str(marker))
        result = subprocess.run(['sh', str(script), 'start'], env=env, capture_output=True, text=True)
        calls = log.read_text()
        assert result.returncode == (1 if case in ['data-failure', 'boot-failure'] else 0), (case, result)
        assert 'parted ' not in calls and 'mkfs.exfat ' not in calls
        if case != 'boot-failure':
            assert marker.exists() == (case in ['recognized', 'mounted']), (case, calls)
        assert ('mount /dev/loop0 ' in calls) == (case in ['recognized', 'data-failure'])
        results.append({'case': case, 'exit': result.returncode, 'calls': calls.splitlines(), 'passed': True})
print(json.dumps(results, indent=2))
