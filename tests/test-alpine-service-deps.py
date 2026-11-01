#!/usr/bin/env python3
"""Check that network access survives NanoKVM storage, module and board failures."""

import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OPENRC = ROOT / "firmware/alpine/openrc"
# Services the base package adds to the default runlevel. Alpine services
# outside this repository (localmount, dbus, sshd, ...) are assumed to start.
DEFAULT = ("nanokvm-storage nanokvm-modules nanokvm-board nanokvm-network "
           "nanokvm-usb nanokvm-usb-internet nanokvm-policy nanokvm-ssh nanokvm-app nanokvm-rescue "
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
# The application starts without waiting for the network interfaces.
assert "nanokvm-network" not in DEPS["nanokvm-app"]["use"] | DEPS["nanokvm-app"]["need"], DEPS["nanokvm-app"]
print("application does not wait for the network: pass")
for failed in ({"nanokvm-storage"}, {"nanokvm-modules"}, {"nanokvm-board"},
               {"nanokvm-modules", "nanokvm-board"}):
    running = started(failed)
    missing = [service for service in REACHABLE if service not in running]
    assert not missing, (failed, missing)
    print(f"{' + '.join(sorted(failed))} failed: network, SSH and mDNS start: pass")
# The SSH rescue starts when the application failed (it checks the server).
assert "nanokvm-rescue" in started({"nanokvm-app"}) and "nanokvm-rescue" in started({"nanokvm-storage"})
print("nanokvm-app failed: SSH rescue starts: pass")
# A media module failure must not skip the identity (hostname, Ethernet MAC).
assert "nanokvm-board" in started({"nanokvm-modules"})
print("nanokvm-modules failed: board identity still applied: pass")
