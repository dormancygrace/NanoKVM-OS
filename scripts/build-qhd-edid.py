#!/usr/bin/env python3
"""Add preferred 2560x1440 ~30 Hz timing to a two-block NanoKVM EDID.

Preserves the monitor identity and existing timings, moving the original
preferred DTD to unused CTA space. This produces a file only; no hardware writes.
"""
import argparse
import hashlib
import json
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--input', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
original = a.input.read_bytes()
if len(original) != 256 or original[:8] != bytes.fromhex('00ffffffffffff00'):
    raise SystemExit('Expected a 256-byte EDID')
if original[126] != 1 or any(sum(original[i:i+128]) % 256 for i in (0,128)):
    raise SystemExit('Invalid original block count/checksums')
if original[128:130] != bytes([2,3]) or not 4 <= original[130] <= 109:
    raise SystemExit('Expected CTA revision 3 with room for detailed timings')
old_dtd = original[54:72]
if old_dtd[:2] == bytes(2):
    raise SystemExit('Expected an existing preferred detailed timing')

# CVT reduced-blanking geometry: 2560/48/32/80 by 1440/3/5/33.
# The 241.5 MHz 59.95055 Hz DTD was independently observed in a VG271U
# EDID. Halving its pixel clock yields 120.75 MHz, 29.975275 Hz while
# preserving sync/porch geometry. This is a proposed HDMI timing, not a
# claim of NanoKVM input qualification.
new_dtd = bytearray.fromhex('2b2f00a0a0a029503020350055502100001a')
new_dtd[12:17] = old_dtd[12:17]  # Keep physical size and border metadata.
data = bytearray(original)
data[54:72] = new_dtd

# Append the old preferred mode without dropping another resolution.
slot = 128 + data[130]
while slot + 18 <= 255 and any(data[slot:slot+18]):
    if data[slot:slot+2] == bytes(2):
        raise SystemExit('Unexpected non-timing CTA descriptor')
    slot += 18
if slot + 18 > 255 or any(data[slot:255]):
    raise SystemExit('No empty CTA detailed-timing slot; review EDID manually')
data[slot:slot+18] = old_dtd

# Existing NanoKVM range limits start at 50 Hz. Make the new 29.975 Hz
# mode fall within the integer vertical range, retaining all other limits.
ranges = [i for i in range(54,126,18) if data[i:i+5] == bytes.fromhex('000000fd00')]
if len(ranges) != 1:
    raise SystemExit('Expected exactly one monitor range descriptor')
data[ranges[0]+5] = min(data[ranges[0]+5],29)
for offset in (0,128):
    data[offset+127] = (-sum(data[offset:offset+127])) & 255
assert all(sum(data[i:i+128]) % 256 == 0 for i in (0,128))
with a.output.open('xb') as f:
    f.write(data)
manifest = dict(input_sha256=hashlib.sha256(original).hexdigest(),
    output_sha256=hashlib.sha256(data).hexdigest(),
    width=2560,height=1440,pixel_clock_hz=120750000,h_total=2720,v_total=1481,
    refresh_hz=120750000/(2720*1481),original_preferred_dtd_new_offset=slot,
    changed_bytes=[i for i in range(256) if original[i] != data[i]],
    checksums_valid=True,note='Generated only; programming, Windows mode discovery and actual HDMI capture need separate verification.')
a.output.with_suffix(a.output.suffix+'.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(json.dumps(manifest,indent=2))
