#!/usr/bin/env python3
"""Build a candidate FIT for the installed NanoKVM loader; never deploy it."""
from pathlib import Path
import gzip
import hashlib
import json
import os
import shutil
import struct
import subprocess

kernel = Path(os.environ['NANOKVM_KERNEL_OUTPUT']).resolve()
ramdisk = Path(os.environ['NANOKVM_INITRAMFS_OUTPUT']).resolve()
out = Path(os.environ['NANOKVM_FIT_OUTPUT']).resolve()
mkimage = Path(os.environ['NANOKVM_MKIMAGE']).resolve()
dumpimage = mkimage.with_name('dumpimage')
epoch = os.environ.get('SOURCE_DATE_EPOCH', '0')
out.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, SOURCE_DATE_EPOCH=epoch)
def run(*args):
    return subprocess.check_output(args, text=True, cwd=out, env=env)
def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

version = run(str(mkimage), '-V').strip()
if version != 'mkimage version 2026.07':
    raise SystemExit('Expected current pinned host mkimage 2026.07')
data = (kernel / 'arch/riscv/boot/Image').read_bytes()
if data[48:56] != b'RISCV\0\0\0':
    raise SystemExit('Not a RISC-V Image')
text_offset, footprint = struct.unpack_from('<QQ', data, 8)
if text_offset != 0x200000 or footprint < len(data):
    raise SystemExit('Unexpected kernel header layout')
load, fit_input, old_initial_sp = 0x80200000, 0x81800000, 0x82800000
if load + footprint > fit_input:
    raise SystemExit('Kernel memory overlaps installed loader FIT input; revise layout')
compression = os.environ.get('NANOKVM_KERNEL_COMPRESSION', 'gzip')
if compression == 'gzip':
    kernel_payload = 'Image.gz'
    compressed = gzip.compress(data, mtime=int(epoch))
elif compression == 'zstd':
    kernel_payload = 'Image.zst'
    compressed = subprocess.check_output(
        ['zstd', '-19', '--single-thread', '--no-progress', '--stdout'], input=data)
else:
    raise SystemExit('NANOKVM_KERNEL_COMPRESSION must be gzip or zstd')
(out / kernel_payload).write_bytes(compressed)
shutil.copyfile(ramdisk / 'initramfs.cpio.gz', out / 'initramfs.cpio.gz')
gzip.decompress((out / 'initramfs.cpio.gz').read_bytes())
shutil.copyfile(kernel / 'arch/riscv/boot/dts/sophgo/sg2002-nanokvm-enhanced.dtb', out / 'board.dtb')
its = '''/dts-v1/;
/ {
    description = "NanoKVM OS candidate - hardware qualification pending";
    #address-cells = <1>;
    images {
        kernel-1 {
            description = "NanoKVM OS Linux";
            data = /incbin/("Image.gz");
            type = "kernel";
            arch = "riscv";
            os = "linux";
            compression = "gzip";
            load = <0x80200000>;
            entry = <0x80200000>;
            hash-1 { algo = "sha256"; };
        };
        ramdisk-1 {
            description = "Adapted stock init with current Buildroot userspace";
            data = /incbin/("initramfs.cpio.gz");
            type = "ramdisk";
            arch = "riscv";
            os = "linux";
            // Linux unpacks gzip cpio; U-Boot passes these bytes unchanged.
            compression = "none";
            hash-1 { algo = "sha256"; };
        };
        fdt-sg2002_licheervnano_sd_minimal {
            description = "NanoKVM OS SG2002 board";
            data = /incbin/("board.dtb");
            type = "flat_dt";
            arch = "riscv";
            compression = "none";
            hash-1 { algo = "sha256"; };
        };
    };
    configurations {
        default = "config-sg2002_licheervnano_sd_minimal";
        config-sg2002_licheervnano_sd_minimal {
            description = "NanoKVM OS with installed loader selection";
            kernel = "kernel-1";
            ramdisk = "ramdisk-1";
            fdt = "fdt-sg2002_licheervnano_sd_minimal";
        };
    };
};
'''
its = its.replace('Image.gz', kernel_payload).replace('compression = "gzip";', 'compression = "' + compression + '";')
(out / 'boot.its').write_text(its)
fit = out / 'boot.sd.candidate'
listing = run(str(mkimage), '-f', 'boot.its', str(fit))
(out / 'fit-listing.txt').write_text(listing)
size = fit.stat().st_size
if fit_input + size > old_initial_sp:
    raise SystemExit('FIT exceeds audited input window')
if size > 11002140 + 5142 * 1024:
    raise SystemExit('FIT exceeds audited boot replacement space')
payloads = [kernel_payload, 'initramfs.cpio.gz', 'board.dtb']
for index, name in enumerate(payloads):
    extracted = out / ('extracted-' + name)
    run(str(dumpimage), '-T', 'flat_dt', '-p', str(index), '-o', str(extracted), str(fit))
    if extracted.read_bytes() != (out / name).read_bytes():
        raise SystemExit('FIT payload mismatch: ' + name)
extracted_kernel = (out / ('extracted-' + kernel_payload)).read_bytes()
if compression == 'gzip':
    decoded_kernel = gzip.decompress(extracted_kernel)
else:
    decoded_kernel = subprocess.check_output(['zstd', '-d', '--stdout'], input=extracted_kernel)
if decoded_kernel != data:
    raise SystemExit('Kernel decompression mismatch')
manifest = {
    'status': 'candidate-not-boot-qualified', 'mkimage': version, 'epoch': int(epoch),
    'kernel_compression': compression,
    'fit_bytes': size, 'fit_sha256': sha(fit),
    'payloads': {name: {'bytes': (out/name).stat().st_size, 'sha256': sha(out/name)} for name in payloads},
    'kernel_image_bytes': len(data), 'kernel_footprint_bytes': footprint,
    'kernel_range': [hex(load), hex(load + footprint)],
    'fit_input_range': [hex(fit_input), hex(fit_input + size)],
    'kernel_to_fit_gap_bytes': fit_input - load - footprint,
    'fit_to_initial_stack_gap_bytes': old_initial_sp - fit_input - size,
    'kernel_decompression_roundtrip': True, 'embedded_payload_roundtrip': True,
    'runtime_lmb_addresses_verified': False, 'new_kernel_booted': False,
}
(out / 'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
print(json.dumps(manifest, indent=2))
