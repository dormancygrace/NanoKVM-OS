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
    output = directory / "environment"
    fixture = definitions
    command = directory / "command"
    fixture += '\nnohup() { printf "%s" "$NANOKVM_SRTP_AES" > "' + str(output) + '"; printf "%s" "$*" > "' + str(command) + '"; }\n'
    fixture += "start_background /kvmapp/server/NanoKVM-Server\nwait\n"
    for requested, expected in [(None, "hardware"), ("", "hardware"), ("software", "software"), ("hardware", "hardware")]:
        env = os.environ.copy()
        env.pop("NANOKVM_SRTP_AES", None)
        if requested is not None:
            env["NANOKVM_SRTP_AES"] = requested
        subprocess.run(["sh"], input=fixture, text=True, env=env, check=True)
        assert output.read_text() == expected, requested
        print(f"PASS AES request={requested!r}: {expected}")
    # The server starts at the priority it raises itself to.
    started = command.read_text()
    assert started.startswith("nice -n -10 /kvmapp/system/init.d/nanokvm-server-supervisor run "), started
    print("PASS server starts at nice -10")
