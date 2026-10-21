#!/usr/bin/env python3
"""Check how the Alpine application service sees the server listen."""

import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile

SERVICE = Path(__file__).resolve().parents[1] / "firmware/alpine/openrc/nanokvm-app"
HEADER = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"


def row(local, state, inode=12345):
    return (f"   0: {local} 00000000:0000 {state} 00000000:00000000 00:00000000 "
            f"00000000     0        0 {inode} 1 0000000000000000 100 0 0 10 0\n")


SHELL = ["sh"]


def run(config, tcp, tcp6=None, fallback=False, server=True):
    with tempfile.TemporaryDirectory(prefix="nkos-app-wait-") as directory:
        root = Path(directory)
        # Only fallback cases may run pidof/ls during polling. A marker also
        # verifies that the normal path does not invoke these external tools.
        bindir = root / "bin"
        bindir.mkdir()
        marker = root / "pidof-called"
        pidof = bindir / "pidof"
        pidof.write_text('#!/bin/sh\necho called > "$MARKER"\n' +
                         ("echo 42\n" if server else "exit 1\n"))
        pidof.chmod(0o755)
        fd = root / "42/fd"
        fd.mkdir(parents=True)
        (fd / "3").symlink_to("socket:[12345]")
        net = root / "net"
        net.mkdir()
        (net / "tcp").write_text(HEADER + "".join(tcp))
        if tcp6 is not None:
            (net / "tcp6").write_text(HEADER + "".join(tcp6))
        config_file = root / "server.yaml"
        if config is not None:
            config_file.write_text(config)
        env = dict(os.environ, SERVICE=str(SERVICE), NANOKVM_PROC_NET=str(net),
                   NANOKVM_SERVER_CONFIG=str(config_file), NANOKVM_PROC_ROOT=str(root),
                   PATH=str(bindir) + os.pathsep + os.environ["PATH"], MARKER=str(marker), PIDOF_STUB=str(pidof))
        result = subprocess.run(
            SHELL + ["-c", 'pidof() { "$PIDOF_STUB"; }; . "$SERVICE"; ports=$(server_ports) || ports=; echo "$ports"; '
             'server_listening $ports && echo listening || echo waiting'],
            env=env, capture_output=True, text=True, check=True)
        assert marker.exists() == fallback, (config, result.stdout, result.stderr)
        assert not result.stderr, result.stderr
        return result.stdout.split("\n")[:2]


CONFIG = "proto: https\nport:\n    http: 8080\n    https: 8443\ncert:\n    crt: x\n"

def checks():
    assert run(None, [])[0] == "0050 01BB", "default ports"
    assert run(CONFIG, [])[0] == "1F90 20FB", "configured ports"
    assert run(None, [row("00000000:01BB", "0A")])[1] == "listening", "https on IPv4"
    assert run(None, [], [row("00000000000000000000000000000000:0050", "0A")])[1] == "listening", "http on IPv6"
    assert run(None, [row("00000000:0016", "0A")])[1] == "waiting", "another listener"
    assert run(None, [row("0100007F:01BB", "01")])[1] == "waiting", "connection, not a listener"
    assert run(CONFIG, [row("00000000:01BB", "0A")])[1] == "waiting", "default port with a configured one"
    assert run(CONFIG, [row("00000000:20FB", "0A")])[1] == "listening", "configured https port"


    for config in (
        'port:\n  http: "8080"\n  https: "8443"\n',
        "port: {http: 8080, https: 8443}\n",
        "ports: &ports {http: 8080, https: 8443}\nport: *ports\n",
        "port:\n  http: 0x1f90\n  https: 0x20fb\n",
        "port:\n  http: 8080\n",  # Invalid config: server falls back to defaults.
    ):
        assert run(config, [row("00000000:20FB", "0A")], fallback=True) == ["", "listening"], config
        assert run(config, [], [row("00000000000000000000000000000000:20FB", "0A")],
                   fallback=True)[1] == "listening", config
        assert run(config, [row("00000000:01BB", "0A")], fallback=True)[1] == "listening"
        assert run(config, [row("00000000:20FB", "0A", inode=912345)],
                   fallback=True)[1] == "waiting", "another process must not match"
        assert run(config, [row("00000000:20FB", "01")],
                   fallback=True)[1] == "waiting", "connected socket is not ready"
        assert run(config, [row("00000000:20FB", "0A")],
                   fallback=True, server=False)[1] == "waiting", "no server process"
    assert run(CONFIG.replace("8080", "8080 # http"), [])[0] == "1F90 20FB"
    # Kernel tables use whitespace-separated fields, not a fixed column width.
    assert run(CONFIG, [row("00000000:20FB", "0A").replace(" ", "  ")])[1] == "listening"


def live_socket():
    # Exercise actual /proc/net layout and fd symlinks without touching a device.
    with socket.socket() as listener, tempfile.TemporaryDirectory(prefix="nkos-app-live-") as directory:
        listener.bind(("127.0.0.1", 0))
        listener.listen()
        port = listener.getsockname()[1]
        config = Path(directory) / "server.yaml"
        for text in (f"port:\n  http: {port}\n  https: {port}\n",
                     f"port: {{http: {port}, https: {port}}}\n"):
            config.write_text(text)
            env = dict(os.environ, SERVICE=str(SERVICE), NANOKVM_SERVER_CONFIG=str(config),
                       NANOKVM_PROC_ROOT="/proc", NANOKVM_PROC_NET="/proc/net", TEST_PID=str(os.getpid()))
            subprocess.run(SHELL + ["-c", 'pidof() { echo "$TEST_PID"; }; . "$SERVICE"; '
                            'ports=$(server_ports) || ports=; server_listening $ports'],
                           env=env, check=True)


checks()
live_socket()
print("application socket wait (sh, including real /proc socket): pass")
if shutil.which("busybox"):
    SHELL = ["busybox", "sh"]
    checks()
    live_socket()
    print("application socket wait (BusyBox ash, including real /proc socket): pass")
