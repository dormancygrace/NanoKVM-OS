#!/usr/bin/env python3
"""Recovery precedes legacy configuration; incomplete recovery keeps networking available for repair."""
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="nkos-recovery-boot-") as tmp:
    root = Path(tmp)
    pending = root / "pending.json"
    events = root / "events"
    helper = root / "helper"
    legacy = root / "legacy"
    legacy.mkdir()
    for name in ["S02config", "S10uuid", "S15kvmhwd"]:
        f = legacy / name
        f.write_text(f"#!/bin/sh\necho {name} >> '{events}'\n")
        f.chmod(0o700)
    board = (repo / "firmware/alpine/openrc/nanokvm-board").read_text().replace(
        "/var/lib/nanokvm-recovery/pending.json", str(pending)).replace(
        "/usr/sbin/nkos-update", str(helper))
    script = root / "board"
    script.write_text(board)
    for fail in (False, True):
        events.write_text("")
        pending.touch()
        helper.write_text(f"#!/bin/sh\necho recovery >> '{events}'\nexit {int(fail)}\n")
        helper.chmod(0o700)
        result = subprocess.run(["sh", "-c", f". '{script}'; ebegin() {{ :; }}; ewarn() {{ :; }}; logger() {{ :; }}; eend() {{ return 0; }}; NANOKVM_BOARD_LEGACY_DIR='{legacy}'; start"], capture_output=True)
        assert bool(result.returncode) == fail, result.stderr
        expected = ["recovery"] + ([] if fail else ["S02config", "S10uuid", "S15kvmhwd"])
        assert events.read_text().splitlines() == expected
    # A recovery that succeeded, then a receiver that does not answer: the
    # board warns and starts, so its consumers start without video. Only a
    # failed recovery (above) stops the board.
    events.write_text("")
    pending.touch()
    helper.write_text(f"#!/bin/sh\necho recovery >> '{events}'\nexit 0\n")
    (legacy / "S15kvmhwd").write_text(f"#!/bin/sh\necho S15kvmhwd >> '{events}'\nexit 1\n")
    result = subprocess.run(["sh", "-c", f". '{script}'; ebegin() {{ :; }}; ewarn() {{ echo \"$*\" >&2; }}; logger() {{ :; }}; eend() {{ return \"$1\"; }}; NANOKVM_BOARD_LEGACY_DIR='{legacy}'; start"], capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert "S15kvmhwd" in result.stderr, result.stderr
    assert events.read_text().splitlines() == ["recovery", "S02config", "S10uuid", "S15kvmhwd"]
    network = root / "network"
    network.write_text((repo / "firmware/alpine/openrc/nanokvm-network").read_text().replace(
        "/var/lib/nanokvm-recovery/pending.json", str(pending)))
    result = subprocess.run(["sh", "-c", f". '{network}'; ewarn() {{ :; }}; ebegin() {{ exit 99; }}; start"], capture_output=True)
    assert result.returncode == 99, result.stderr
print("PASS: early recovery ordering and incomplete-recovery remote repair")
