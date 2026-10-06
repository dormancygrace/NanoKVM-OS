#!/usr/bin/env python3
"""Check that network access survives NanoKVM storage, module and board failures."""

import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
OPENRC = ROOT / "firmware/alpine/openrc"
# Services the base package adds to the default runlevel. Alpine services
# outside this repository (localmount, dbus, sshd, ...) are assumed to start.
DEFAULT = ("nanokvm-storage nanokvm-modules nanokvm-board nanokvm-network "
           "nanokvm-usb nanokvm-policy nanokvm-ssh nanokvm-app "
           "nanokvm-watchdog nanokvm-mdns").split()
PROVIDES = {"net": "nanokvm-network"}
REACHABLE = ("nanokvm-network", "nanokvm-ssh", "nanokvm-mdns")


def dependencies(service):
    script = (
        'need() { printf "need %s\\n" "$*"; }; use() { printf "use %s\\n" "$*"; }; '
        'want() { printf "want %s\\n" "$*"; }; after() { :; }; before() { :; }; '
        'provide() { :; }; keyword() { :; }; . "$SERVICE"; depend'
    )
    result = subprocess.run(["sh", "-c", script], capture_output=True, text=True, check=True,
                            env=dict(os.environ, SERVICE=str(OPENRC / service)))
    deps = {"need": set(), "use": set(), "want": set()}
    for line in result.stdout.splitlines():
        kind, *names = line.split()
        deps[kind].update(PROVIDES.get(name, name) for name in names)
    return deps


DEPS = {service: dependencies(service) for service in DEFAULT}


def started(failed):
    """OpenRC starts a service only when it and every service it needs start."""
    state = {}

    def starts(service):
        if service not in DEPS:
            return True
        if service not in state:
            state[service] = False
            state[service] = service not in failed and all(
                starts(need) for need in DEPS[service]["need"])
        return state[service]

    return {service for service in DEFAULT if starts(service)}


assert started(set()) == set(DEFAULT), started(set())
print("all services start: pass")
# OpenRC stops services that need a stopped service; an application restart
# must leave the hang watchdog running.
assert "nanokvm-app" not in DEPS["nanokvm-watchdog"]["need"], DEPS["nanokvm-watchdog"]
print("watchdog survives application restart: pass")
for failed in ({"nanokvm-storage"}, {"nanokvm-modules"}, {"nanokvm-board"},
               {"nanokvm-modules", "nanokvm-board"}):
    running = started(failed)
    missing = [service for service in REACHABLE if service not in running]
    assert not missing, (failed, missing)
    # The application and its watchdog keep requiring complete hardware setup.
    assert "nanokvm-app" not in running and "nanokvm-watchdog" not in running, (failed, running)
    print(f"{' + '.join(sorted(failed))} failed: network, SSH and mDNS start; app stays stopped")

# A media module failure must not skip the identity (hostname, Ethernet MAC).
assert "nanokvm-board" in started({"nanokvm-modules"})
print("nanokvm-modules failed: board identity still applied: pass")


def run_board(fail=(), missing=()):
    with tempfile.TemporaryDirectory(prefix="nkos-openrc-board-") as directory:
        legacy = Path(directory)
        calls = legacy / "calls"
        for script in ("S02config", "S10uuid", "S15kvmhwd"):
            if script in missing:
                continue
            mock = legacy / script
            mock.write_text("#!/bin/sh\n"
                            f"printf '{script} %s\\n' \"$1\" >> \"$CALLS\"\n"
                            + ("exit 1\n" if script in fail else "exit 0\n"))
            mock.chmod(0o755)
        result = subprocess.run(
            ["sh", "-c", '. "$SERVICE"; ebegin() { :; }; '
             'eend() { [ "$1" -eq 0 ] || printf "%s\\n" "$2" >&2; return "$1"; }; start'],
            env=dict(os.environ, SERVICE=str(OPENRC / "nanokvm-board"), CALLS=str(calls),
                     NANOKVM_BOARD_LEGACY_DIR=str(legacy)),
            capture_output=True, text=True,
        )
        return result, calls.read_text().splitlines() if calls.exists() else []


ALL = ["S02config start", "S10uuid start", "S15kvmhwd start"]
result, calls = run_board()
assert result.returncode == 0 and calls == ALL, (result, calls)
print("board setup succeeds: pass")
for fail in ("S02config", "S10uuid", "S15kvmhwd"):
    result, calls = run_board(fail=(fail,))
    assert result.returncode != 0 and calls == ALL, (fail, result, calls)
    assert fail in result.stderr, (fail, result.stderr)
    print(f"{fail} fails: remaining steps run, board reports failure: pass")
result, calls = run_board(missing=("S02config",))
assert result.returncode == 0 and calls == ALL[1:], (result, calls)
print("missing optional step is skipped: pass")
