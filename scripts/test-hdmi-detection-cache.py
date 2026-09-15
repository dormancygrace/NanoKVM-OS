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
    mocks = 'check_board() { declared_board=pcie; board=pcie; }\ndetect_board() { board=pcie; }\ndetect_hdmi() { hdmi=ux; echo probe >> "' + str(root / "probes") + '"; }\n'
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

    # A missing 0x2c response is expected for LT6911D/UX hardware. It must
    # fall through to 0x2b (and then 0x44), whereas a failed i2cdetect command
    # remains fatal instead of being mistaken for a missing receiver.
    source = (Path(__file__).resolve().parents[1] / "firmware/buildroot/board/enhanced/init.d/S15kvmhwd").read_text()
    source = source.replace("/etc/kvm", str(root))
    position = source.index('case "${1:-}" in')
    fallback = root / "fallback"
    fallback.mkdir()
    mocks = f'''check_board() {{ declared_board=pcie; }}
detect_board() {{ board=pcie; }}
i2cdetect() {{
    case "$2:$3" in
        4:0x2c) printf '%s\\n' '30: --';;
        4:0x2b) printf '%s\\n' '20: 2b';;
        4:0x44) printf '%s\\n' '40: 44';;
        *) return 7;;
    esac
}}
publish() {{ printf '%s\\n' "$2" > "{fallback}/$1"; }}
'''
    script.write_text(source[:position] + mocks + source[position:])
    subprocess.run(["sh", str(script), "start"], check=True)
    assert (fallback / "hdmi_version").read_text().strip() == "ux"

    failed = root / "failed-probe"
    mocks = f'''check_board() {{ declared_board=pcie; }}
detect_board() {{ board=pcie; }}
i2cdetect() {{ return 7; }}
publish() {{ touch "{failed}"; }}
'''
    script.write_text(source[:position] + mocks + source[position:])
    failed_run = subprocess.run(["sh", str(script), "start"])
    assert failed_run.returncode != 0, "I2C command failure was accepted"
    assert not failed.exists(), "failed I2C probe published a profile"

    # OLED probes are advisory for the declared PCIe profile.  In particular,
    # an I2C1 command error must not prevent the available I2C5 path from
    # falling back to the existing PCIe behavior when neither OLED responds.
    pcie_fallback = root / "pcie-fallback"
    pcie_fallback.mkdir()
    mocks = f'''check_board() {{ declared_board=pcie; }}
i2c_available() {{ return 0; }}
i2cdetect() {{
    case "$2:$3" in
        1:0x3d) return 7;;
        5:0x3d) printf '%s\\n' '30: --';;
        *) return 7;;
    esac
}}
detect_hdmi() {{ hdmi=ux; }}
publish() {{ printf '%s\\n' "$2" > "{pcie_fallback}/$1"; }}
'''
    script.write_text(source[:position] + mocks + source[position:])
    subprocess.run(["sh", str(script), "start"], check=True)
    assert (pcie_fallback / "hw").read_text().strip() == "pcie"

    # A copied PCIe DT must not force a Cube to use the PCIe OLED address.
    # The actual marker follows the probed address: I2C1/0x3d is alpha,
    # I2C5/0x3d is beta/Cube, and I2C5/0x3c is PCIe.
    source = (Path(__file__).resolve().parents[1] / "firmware/buildroot/board/enhanced/init.d/S15kvmhwd").read_text()
    source = source.replace("/etc/kvm", str(root))
    position = source.index('case "${1:-}" in')
    for expected, addresses in (
        ("alpha", {"1:0x3d"}),
        ("beta", {"5:0x3d"}),
        ("pcie", {"5:0x3c"}),
    ):
        profiles = root / ("profiles-" + expected)
        profiles.mkdir()
        declared = "pcie"  # reproduces the bad historical DT declaration.
        mocks = f'''check_board() {{ declared_board={declared}; }}
i2c_available() {{ return 0; }}
responds_on() {{ case "$1:$2" in {'|'.join(addresses)}) return 0;; *) return 1;; esac; }}
detect_hdmi() {{ hdmi=ux; }}
publish() {{ printf '%s\\n' "$2" > "{profiles}/$1"; }}
'''
        script.write_text(source[:position] + mocks + source[position:])
        subprocess.run(["sh", str(script), "start"], check=True)
        assert (profiles / "hw").read_text().strip() == expected
    # Selected DTs are authoritative even when an optional OLED disappears.
    for profile, legacy in (("alpha", "alpha"), ("beta", "beta"), ("pcie", "pcie"), ("lite", "beta")):
        profiles = root / ("selected-" + profile)
        profiles.mkdir()
        mocks = f'''check_board() {{ declared_board={profile}; profiled=1; }}
responds_on() {{ return 7; }}
detect_hdmi() {{ hdmi=ux; }}
publish() {{ printf '%s\\n' "$2" > "{profiles}/$1"; }}
'''
        script.write_text(source[:position] + mocks + source[position:])
        subprocess.run(["sh", str(script), "start"], check=True)
        assert (profiles / "hw").read_text().strip() == legacy
        assert (profiles / "board-profile").read_text().strip() == profile
print("HDMI cached-model and explicit detection checks passed")
