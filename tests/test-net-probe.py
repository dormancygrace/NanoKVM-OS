#!/usr/bin/env python3
"""Compile the production ICMP helper and exercise parse/timeout/loopback paths."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory() as tmp:
    binary = str(Path(tmp) / 'net-probe')
    subprocess.run([os.environ.get('CXX', 'g++'), '-std=c++17', '-Wall', '-Wextra', '-Werror',
                    '-I', str(root / 'support/sg2002/kvm_system/main/lib/system_state'),
                    str(root / 'tests/test-net-probe.cpp'),
                    str(root / 'support/sg2002/kvm_system/main/lib/system_state/net_probe.cpp'),
                    '-o', binary], check=True)
    subprocess.run([binary], check=True)
