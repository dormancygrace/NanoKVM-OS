#!/usr/bin/env python3
"""Execute production GPIO transport, scheduling and detector service."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
vision = (root / "support/sg2002/additional/kvm/src/kvm_vision.cpp").read_text()
start = vision.index("template<class Events>\nstatic bool service_hdmi_events(")
end = vision.index("\nvoid* vi_subsystem_detection", start)
source = (root / "tests/test-hdmi-events.cpp").read_text().replace("// PRODUCTION_SERVICE", vision[start:end])
with tempfile.TemporaryDirectory(prefix="nanokvm-hdmi-events-") as directory:
    tmp = Path(directory)
    cpp, binary = tmp / "events.cpp", tmp / "events"
    cpp.write_text(source)
    flags = ["-std=c++17", "-Wall", "-Wextra", "-Werror", "-g"]
    if os.getenv("NANOKVM_SANITIZE"):
        flags += ["-fsanitize=address,undefined", "-fno-omit-frame-pointer"]
    subprocess.run(["g++", *flags, "-I" + str(root / "support/sg2002/additional/kvm/include"),
                    str(cpp), "-o", str(binary)], check=True)
    subprocess.run([str(binary)], check=True, timeout=15)
    subprocess.run(["g++", "-std=c++17", "-Wall", "-Wextra", "-Werror", "-fsyntax-only",
                    "-I" + str(root / "support/sg2002/additional/kvm/include"),
                    str(root / "firmware/probes/hdmi-events-runtime.cpp")], check=True)
# The production loop observes events even while manual mode bypasses service.
detector = vision[vision.index("void* vi_subsystem_detection"):vision.index("uint8_t frame_changed")]
assert detector.index("events.observe(") < detector.index("switch (kvmv_cfg.hdmi_mode)")
assert "service_hdmi_events(events, now, vi_state_refreshed, last_vi_state_code)" in detector
assert "/proc/lt_int" not in vision
