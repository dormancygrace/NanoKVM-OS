#!/usr/bin/env python3
"""Turn the per-board FIT images into one template plus device trees.

usage: fit-layout.py FIT_DIR DTB_DIR OUTPUT_DIR

FIT_DIR holds NAME.sd built by mkimage and DTB_DIR the NAME.dtb inside each
(all padded to one size). Writes fit-template.sd, fit.layout, NAME.dtb,
NAME.fdt-sha256 and NAME.sha256 (the hash of the full image), and checks that
compose-fit's substitution reproduces every image byte for byte."""
import hashlib
import pathlib
import shutil
import sys

fits, dtbs, out = (pathlib.Path(a) for a in sys.argv[1:4])
names = sorted(p.stem for p in fits.glob('*.sd'))
if not names:
    sys.exit('no FIT images')


def locate(blob, needle, what):
    first = blob.find(needle)
    if first < 0 or blob.find(needle, first + 1) >= 0:
        sys.exit(f'{what}: not exactly once in the FIT')
    return first


layout = None
images = {}
for name in names:
    fit = (fits / f'{name}.sd').read_bytes()
    dtb = (dtbs / f'{name}.dtb').read_bytes()
    digest = hashlib.sha256(dtb).digest()
    here = (locate(fit, dtb, f'{name} device tree'), len(dtb), locate(fit, digest, f'{name} device tree hash'), len(fit))
    if layout is None:
        layout = here
    elif here != layout:
        sys.exit(f'{name}: FIT layout {here} differs from {layout}')
    images[name] = (fit, dtb, digest)

dtb_offset, dtb_size, hash_offset, _ = layout
template = images[names[0]][0]
for name, (fit, dtb, digest) in images.items():
    composed = bytearray(template)
    composed[dtb_offset:dtb_offset + dtb_size] = dtb
    composed[hash_offset:hash_offset + 32] = digest
    if bytes(composed) != fit:
        sys.exit(f'{name}: composing from the template does not reproduce the FIT')

out.mkdir(parents=True, exist_ok=True)
shutil.copyfile(fits / f'{names[0]}.sd', out / 'fit-template.sd')
(out / 'fit.layout').write_text(f'DTB_OFFSET={dtb_offset}\nDTB_SIZE={dtb_size}\nHASH_OFFSET={hash_offset}\n')
for name, (fit, dtb, digest) in images.items():
    (out / f'{name}.dtb').write_bytes(dtb)
    (out / f'{name}.fdt-sha256').write_bytes(digest)
    (out / f'{name}.sha256').write_text(f'{hashlib.sha256(fit).hexdigest()}  {name}.sd\n')
print(f'{len(names)} boot images from one {len(template)}-byte template')
