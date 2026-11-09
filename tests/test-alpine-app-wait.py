#!/usr/bin/env python3
"""The Alpine application service waits for the server's listening marker."""

import os
from pathlib import Path
import subprocess
import tempfile
import time

SERVICE = Path(__file__).resolve().parents[1] / "firmware/alpine/openrc/nanokvm-app"


def start(marker_after):
    """Run start(); a stub S95 writes the marker after marker_after seconds (None: never)."""
    with tempfile.TemporaryDirectory(prefix="nkos-app-wait-") as directory:
        root = Path(directory)
        marker = root / "listening"
        app = root / "S95nanokvm"
        body = "" if marker_after is None else f'(sleep {marker_after}; : > "{marker}") &\n'
        app.write_text("#!/bin/sh\n" + body)
        app.chmod(0o755)
        env = dict(os.environ, SERVICE=str(SERVICE), NANOKVM_SERVER_LISTENING=str(marker),
                   NANOKVM_APP_START=str(app))
        began = time.monotonic()
        result = subprocess.run(
            ["sh", "-c", 'ebegin() { :; }; eend() { return "${1:-0}"; }; . "$SERVICE"; start'],
            env=env, capture_output=True, text=True, timeout=30)
        assert result.returncode == 0 and not result.stderr, result
        return time.monotonic() - began


def checks():
    assert start(0.3) < 2, "returns soon after the server listens"
    assert 14 < start(None) < 25, "gives up after about 15 s"


checks()
print("PASS: nanokvm-app waits for the listening marker")
