#!/usr/bin/env python3
"""The OLED editor's font (web/src/lib/oled/font.ts) matches kvm_system's."""
from pathlib import Path
import subprocess, sys
repo = Path(__file__).resolve().parents[1]
subprocess.run([sys.executable, str(repo / 'scripts/gen-oled-web-font.py'), '--check'], check=True)
