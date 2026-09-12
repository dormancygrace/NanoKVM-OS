#!/usr/bin/env python3
"""Scan exact release inputs for explicitly forbidden build paths.

This bounded byte scan is not a complete secret, license or runtime audit.
Use decompressed images and payloads; compressed bytes can hide matching text.
"""
import argparse
import hashlib
import json
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--input', type=Path, action='append', required=True)
p.add_argument('--forbid-text', action='append', required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
patterns = [x.encode() for x in a.forbid_text]
if any(not x for x in patterns):
    p.error('Empty markers are not meaningful')
longest = max(map(len, patterns))
results = []
for source in a.input:
    if not source.is_file():
        p.error('Input must be a regular file: '+str(source))
    digest = hashlib.sha256()
    found = set()
    size = 0
    tail = b''
    with source.open('rb') as stream:
        while block := stream.read(8 * 1024 * 1024):
            digest.update(block)
            size += len(block)
            data = tail + block
            for index, pattern in enumerate(patterns):
                if pattern in data:
                    found.add(index)
            tail = data[-(longest - 1):] if longest > 1 else b''
    # Report marker indices only; callers must not put secrets in CLI arguments.
    results.append(dict(input=source.name, bytes=size, sha256=digest.hexdigest(),
                        forbidden_marker_indices=sorted(found)))
report = dict(scope='Explicit byte markers in provided uncompressed inputs only',
              qualification='Not a complete privacy/license/runtime qualification',
              marker_count=len(patterns), inputs=results,
              passed=all(not x['forbidden_marker_indices'] for x in results))
a.output.write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
raise SystemExit(0 if report['passed'] else 1)
