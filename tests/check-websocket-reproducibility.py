#!/usr/bin/env python3
"""Compare the patched dependency binary from two differently named build roots."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("websocket_prepare", ROOT / "firmware/cpu/go-websocket-c906/prepare.py")
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    go = args.go.absolute()
    hashes = []
    env = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", CGO_ENABLED="0")
    for name in ("short", "different-long-build-root"):
        output = args.output.resolve() / name
        modfile = prepare.prepare(go, ROOT / "server", output)
        project = modfile.parent
        module = json.loads(subprocess.check_output([str(go), "mod", "edit", "-json"], cwd=project, env=env))
        for entry in module.get("Replace", []):
            if not entry["New"].get("Version"):
                assert not Path(entry["New"]["Path"]).is_absolute(), entry
        probe = project / "reprocheck"
        probe.mkdir()
        (probe / "main.go").write_text('''package main
import (
    "github.com/gorilla/websocket"
    _ "github.com/pion/dtls/v3"
    _ "github.com/pion/ice/v4"
    _ "github.com/pion/srtp/v3"
)
func main() {
    if _, err := websocket.NewPreparedMessage(websocket.BinaryMessage, []byte("reproducible")); err != nil { panic(err) }
}
''')
        binary = output / "probe"
        subprocess.run([str(go), "build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid=", "-o", str(binary), "./reprocheck"], cwd=project, env=env, check=True)
        hashes.append(hashlib.sha256(binary.read_bytes()).hexdigest())
        info = subprocess.check_output([str(go), "version", "-m", str(binary)], env=env, text=True)
        assert str(output) not in "\n".join(info.splitlines()[1:]), info
    assert hashes[0] == hashes[1], hashes
    print("PASS: identical binaries across build roots:", hashes[0])


if __name__ == "__main__":
    main()
