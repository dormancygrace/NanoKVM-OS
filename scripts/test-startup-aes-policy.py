#!/usr/bin/env python3
"""Execute startup policy: default hardware AES, preserve explicit overrides."""
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
source = (root / "kvmapp/system/init.d/S95nanokvm").read_text()
definitions = source.split('case "$1" in\n', 1)[0]
with tempfile.TemporaryDirectory() as directory:
    directory = Path(directory)
    marker = directory / "buildroot"
    marker.write_text("flavour=enhanced\n")
    output = directory / "environment"
    fixture = definitions.replace("/etc/nanokvm-buildroot", str(marker))
    fixture += '\nnohup() { printf "%s" "$NANOKVM_SRTP_AES" > "' + str(output) + '"; }\n'
    fixture += "start_background /tmp/server/NanoKVM-Server\nwait\n"
    for requested, expected in [(None, "hardware"), ("", "hardware"), ("software", "software"), ("hardware", "hardware")]:
        env = os.environ.copy()
        env.pop("NANOKVM_SRTP_AES", None)
        if requested is not None:
            env["NANOKVM_SRTP_AES"] = requested
        subprocess.run(["sh"], input=fixture, text=True, env=env, check=True)
        assert output.read_text() == expected, requested
        print(f"PASS AES request={requested!r}: {expected}")
