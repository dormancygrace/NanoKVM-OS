#!/usr/bin/env python3
"""Check how the Alpine application service sees the server listen."""

import os
from pathlib import Path
import subprocess
import tempfile

SERVICE = Path(__file__).resolve().parents[1] / "firmware/alpine/openrc/nanokvm-app"
HEADER = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"


def row(local, state):
    return (f"   0: {local} 00000000:0000 {state} 00000000:00000000 00:00000000 "
            "00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n")


def run(config, tcp, tcp6=None):
    with tempfile.TemporaryDirectory(prefix="nkos-app-wait-") as directory:
        root = Path(directory)
        net = root / "net"
        net.mkdir()
        (net / "tcp").write_text(HEADER + "".join(tcp))
        if tcp6 is not None:
            (net / "tcp6").write_text(HEADER + "".join(tcp6))
        config_file = root / "server.yaml"
        if config is not None:
            config_file.write_text(config)
        env = dict(os.environ, SERVICE=str(SERVICE), NANOKVM_PROC_NET=str(net),
                   NANOKVM_SERVER_CONFIG=str(config_file))
        result = subprocess.run(
            ["sh", "-c", '. "$SERVICE"; ports=$(server_ports); echo "$ports"; '
             'server_listening $ports && echo listening || echo waiting'],
            env=env, capture_output=True, text=True, check=True)
        return result.stdout.split("\n")[:2]


CONFIG = "proto: https\nport:\n    http: 8080\n    https: 8443\ncert:\n    crt: x\n"

assert run(None, [])[0] == "0050 01BB", "default ports"
assert run(CONFIG, [])[0] == "1F90 20FB", "configured ports"
assert run(None, [row("00000000:01BB", "0A")])[1] == "listening", "https on IPv4"
assert run(None, [], [row("00000000000000000000000000000000:0050", "0A")])[1] == "listening", "http on IPv6"
assert run(None, [row("00000000:0016", "0A")])[1] == "waiting", "another listener"
assert run(None, [row("0100007F:01BB", "01")])[1] == "waiting", "connection, not a listener"
assert run(CONFIG, [row("00000000:01BB", "0A")])[1] == "waiting", "default port with a configured one"
assert run(CONFIG, [row("00000000:20FB", "0A")])[1] == "listening", "configured https port"
print("application wait for the server socket: pass")
