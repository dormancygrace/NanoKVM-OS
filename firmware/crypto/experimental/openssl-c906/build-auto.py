#!/usr/bin/env python3
"""Isolated compiler memory-operation experiment; never used by the image build."""
import importlib.util
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[4]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('openssl_builder', ROOT / 'scripts/build-openssl.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
production_flags = builder.flags
production_record = builder.record


def flags(kind):
    return [f for f in production_flags(kind) if not f.startswith('-mstringop-strategy=')] + ['-mstringop-strategy=auto']


def record(output, compiler, kind, actual):
    # Check all other production constraints, then record the actual experiment.
    checked = [f for f in actual if not f.startswith('-mstringop-strategy=')] + ['-mstringop-strategy=scalar']
    production_record(output, compiler, kind, checked)
    path = Path(output)
    data = json.loads(path.read_text())
    data['flags'] = actual
    data['experimental'] = 'Only compiler stringop strategy changed to auto for isolated measurement'
    path.write_text(json.dumps(data, indent=2) + '\n')


if __name__ == '__main__':
    builder.flags = flags
    builder.record = record
    builder.main()
