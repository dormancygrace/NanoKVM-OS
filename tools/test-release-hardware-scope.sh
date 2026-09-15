#!/bin/sh
# Keep release guidance aligned with the PCIe-only Enhanced boot assets.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
HWD="$ROOT/firmware/buildroot/board/enhanced/init.d/S15kvmhwd"

# Historical beta10/beta11 artifacts are PCIe-only.  The next source tree
# detects board type from the OLED address, retaining the PCIe branch without
# trusting a copied DT declaration for Cube hardware.
grep -Fq '[ "$declared_board" = pcie ]' "$HWD"
grep -Fq '("beta", {"5:0x3d"})' "$ROOT/scripts/test-hdmi-detection-cache.py"
grep -Fq 'NanoKVM Enhanced PCIe/UXC only' "$ROOT/docs/RELEASE-beta-11.md"
grep -Fq 'not a Cube Full image' "$ROOT/docs/RELEASE-beta-11.md"
grep -Fq 'not a Cube Full firmware release' "$ROOT/docs/RELEASE-beta-10.md"
grep -Fq 'Do not install the `.nkos` package on a Cube Full.' "$ROOT/docs/RELEASE-beta-10.md"

echo 'release hardware-scope documentation tests passed'
