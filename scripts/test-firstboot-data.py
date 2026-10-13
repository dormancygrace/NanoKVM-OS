#!/usr/bin/env python3
"""Run the synthetic firstboot data lifecycle tests."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
subprocess.run(["sh", str(root / "tools/test-s01fs-data-lifecycle.sh")], check=True)
