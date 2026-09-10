#!/usr/bin/env python3
"""Exercise S95 receiver preparation against a temporary reset control file.

This checks shell policy only. Real I2C/VI recovery requires device/browser tests.
"""
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
source = (root / "kvmapp/system/init.d/S95nanokvm").read_text()
definitions = source.split('case "$1" in\n', 1)[0]
with tempfile.TemporaryDirectory() as directory:
    reset = Path(directory) / "reset"
    calls = Path(directory) / "calls"
    fixture = definitions.replace(
        "/sys/bus/platform/devices/hdmi-reset/asserted", str(reset)
    ) + f'\nsleep() {{ echo "$1" >> "{calls}"; }}\nprepare_hdmi_receiver\n'
    for state, expected, status, wait in [
        ("1", "0", 0, True),
        ("0", "0", 0, False),
        ("invalid", "invalid", 1, False),
        (None, None, 0, False),
    ]:
        reset.unlink(missing_ok=True)
        calls.unlink(missing_ok=True)
        if state is not None:
            reset.write_text(state)
        result = subprocess.run(["sh"], input=fixture, text=True, capture_output=True)
        assert result.returncode == status, (state, result.stderr)
        assert (reset.read_text() if reset.exists() else None) == expected
        assert (calls.read_text() if calls.exists() else "") == ("1\n" if wait else "")
        print(f"PASS reset={state!r}, exit={status}, warmup={wait}")
