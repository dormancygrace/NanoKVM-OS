#!/usr/bin/env python3
"""Exercise crash restart, bounded records, duplicate start and intentional stop."""
from pathlib import Path
import os
import subprocess
import tempfile
import time

root = Path(__file__).resolve().parents[1]
script = root / 'kvmapp/system/init.d/nanokvm-server-supervisor'

def until(predicate, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(.03)
    raise AssertionError('timed out')

with tempfile.TemporaryDirectory() as tmp:
    state = Path(tmp)
    env = {**os.environ, 'NANOKVM_RECOVERY_DIR': tmp}
    child = state / 'server'
    child.write_text("""#!/bin/sh
state=$NANOKVM_RECOVERY_DIR
n=$(cat "$state/count" 2>/dev/null || echo 0)
n=$((n+1))
printf '%s' "$n" > "$state/count"
printf ready > "$state/video-phase"
if [ "$n" -le 3 ]; then
    head -c 40000 /dev/zero > "$state/crash.txt"
    exit 139
fi
trap 'exit 0' TERM
while :; do sleep 1; done
""")
    child.chmod(0o755)
    process = subprocess.Popen(['sh', str(script), 'run', str(child)], env=env)
    try:
        until(lambda: (state/'count').exists() and (state/'count').read_text() == '4')
        records = list(state.glob('exit-*.txt'))
        assert len(records) == 3
        assert all(16384 < p.stat().st_size < 16480 for p in records)
        # Duplicate starts do not fork a second child.
        subprocess.run(['sh', str(script), 'run', str(child)], env=env, check=True, timeout=3)
        assert (state/'count').read_text() == '4'
        subprocess.run(['sh', str(script), 'stop'], env=env, check=True)
        process.wait(timeout=4)
        subprocess.run(['sh', str(script), 'wait'], env=env, check=True)
        assert (state/'count').read_text() == '4'
        assert len(list(state.glob('exit-*.txt'))) == 3, 'intentional stop recorded as a crash'
        assert not (state/'supervisor.pid').exists()
        # A reused pid belonging to another process must never be signalled.
        (state/'supervisor.pid').write_text(str(os.getpid()))
        subprocess.run(['sh', str(script), 'stop'], env=env, check=True)
    finally:
        if process.poll() is None:
            subprocess.run(['sh', str(script), 'stop'], env=env, check=True)
            process.wait(timeout=4)
print('PASS supervisor restart, bounded records, duplicate start, stop and PID reuse')
