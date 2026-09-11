#!/usr/bin/env python3
"""Identify the immutable kernel/module and musl foundation of system packages."""
from pathlib import Path
import hashlib, sys
root, board = map(Path, sys.argv[1:])
def digest(p):
    with p.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()
modules = board / 'usr/lib/modules'
rows = ['musl:' + digest(root / 'usr/lib/libc.so'),
        'kernel:' + (board / 'kernel.release').read_text().strip()]
rows += [str(p.relative_to(modules)) + ':' + digest(p)
         for p in sorted(modules.rglob('*.ko'))]
assert len(rows) > 2
value = hashlib.sha256(('\n'.join(rows) + '\n').encode()).hexdigest()
(root / 'etc/nkos-system-base').write_text(value + '\n')
