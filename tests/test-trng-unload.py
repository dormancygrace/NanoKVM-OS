#!/usr/bin/env python3
"""nanokvm-modules unloads the TRNG in its background part once the random
pool is ready, and leaves it alone when it is not loaded or the pool blocks."""
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SERVICE = (ROOT / 'firmware/alpine/openrc/nanokvm-modules').read_text()

with tempfile.TemporaryDirectory() as directory:
    work = Path(directory)
    root = work / 'root'
    bindir = work / 'bin'
    bindir.mkdir()
    log = work / 'modprobe.log'
    (bindir / 'modprobe').write_text(f'#!/bin/sh\necho "$*" >> {log}\n')
    (bindir / 'modprobe').chmod(0o755)
    # Only the test copy reads the temporary root.
    script = SERVICE.replace('/sys/', f'{root}/sys/').replace('/dev/random', f'{root}/dev/random')
    script = script.replace('/kvmapp/', f'{root}/kvmapp/')
    (root / 'dev').mkdir(parents=True)
    (root / 'kvmapp').mkdir()

    def start(loaded, ready):
        log.unlink(missing_ok=True)
        module = root / 'sys/module/cv181x_trng'
        if loaded:
            module.mkdir(parents=True, exist_ok=True)
        elif module.exists():
            module.rmdir()
        random = root / 'dev/random'
        random.unlink(missing_ok=True)
        if ready:
            random.write_bytes(b'\x5a' * 16)
        else:
            # A FIFO without a writer blocks like /dev/random before the pool is ready.
            subprocess.run(['mkfifo', str(random)], check=True)
        runner = 'ebegin() { :; }\neend() { :; }\n' + script + '\nstart\nwait\n'
        subprocess.run(['sh'], input=runner, text=True, check=True, timeout=30,
                       env={'PATH': f'{bindir}:/usr/bin:/bin'})
        return [line for line in (log.read_text().splitlines() if log.exists() else []) if 'cv181x_trng' in line]

    assert start(loaded=True, ready=True) == ['-r cv181x_trng'], 'unloaded once the pool is ready'
    assert start(loaded=False, ready=True) == [], 'not loaded: nothing to do'
    assert start(loaded=True, ready=False) == [], 'pool not ready: kept'
print('PASS: nanokvm-modules unloads cv181x_trng once /dev/random is ready, only then and only when loaded')
