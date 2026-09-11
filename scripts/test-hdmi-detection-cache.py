#!/usr/bin/env python3
"""Exercise the real init-script dispatch with mocked board/probe operations."""
from pathlib import Path
import subprocess
import tempfile

source = (Path(__file__).resolve().parents[1] / "firmware/buildroot/board/enhanced/init.d/S15kvmhwd").read_text()
with tempfile.TemporaryDirectory() as name:
    root = Path(name)
    script = root / "detect.sh"
    source = source.replace("/etc/kvm", str(root))
    position = source.index('case "${1:-}" in')
    mocks = 'check_board() { board=pcie; }\ndetect_hdmi() { hdmi=ux; echo probe >> "' + str(root / "probes") + '"; }\n'
    script.write_text(source[:position] + mocks + source[position:])
    version = root / "hdmi_version"
    probes = root / "probes"
    for kind in ("c", "ux", "d", "ue"):
        version.write_text(kind + "\n")
        subprocess.run(["sh", str(script), "get_hdmi_version"], check=True)
        assert version.read_text().strip() == kind
        assert not probes.exists(), "cached model was needlessly probed"
    for invalid in (None, "", "unknown"):
        if invalid is None:
            version.unlink()
        else:
            version.write_text(invalid)
        subprocess.run(["sh", str(script), "get_hdmi_version"], check=True)
        assert version.read_text().strip() == "ux"
        assert probes.read_text().splitlines() == ["probe"]
        probes.unlink()
    subprocess.run(["sh", str(script), "re-detect"], check=True)
    assert probes.read_text().splitlines() == ["probe"], "explicit re-detect skipped probe"
print("HDMI cached-model and explicit detection checks passed")
