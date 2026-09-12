#!/usr/bin/env python3
"""Prepare, never install, an SD 50 MHz diagnostic FIT from the verified RC4 FIT.

Usage: build-sd50-diagnostic.py SOURCE_FIT NEW_OUTPUT_DIR DUMPIMAGE
Requires fdtget/fdtput and the existing loader's dumpimage. Only the embedded
SD0 max-frequency cell and its FIT SHA-256 change; kernel/ramdisk stay identical.
"""
import hashlib
import json
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import zlib

SOURCE_SHA = '3f29907f8cba79c85a24402d5484a946ab8c1045b03caa2ff167536c8ec4e42f'
FDT_NODE = '/images/fdt-sg2002_licheervnano_sd_minimal'


def run(*args):
    return subprocess.check_output([str(x) for x in args], text=True).strip()


def unique_offset(data, value):
    assert data.count(value) == 1, 'Expected unique embedded value'
    return data.index(value)


def main():
    source, out, dumpimage = (Path(x).resolve() for x in sys.argv[1:])
    original = source.read_bytes()
    assert hashlib.sha256(original).hexdigest() == SOURCE_SHA, 'Unexpected source FIT'
    out.mkdir(parents=True, exist_ok=False)
    payloads = []
    for index, name in enumerate(['kernel.bin', 'ramdisk.bin', 'board-original.dtb']):
        path = out / name
        run(dumpimage, '-T', 'flat_dt', '-p', index, '-o', path, source)
        payloads.append(path.read_bytes())
    for index, name in enumerate(['kernel-1', 'ramdisk-1']):
        node = '/images/' + name
        hash_node = run('fdtget', '-l', source, node).splitlines()
        assert len(hash_node) == 1
        assert run('fdtget', source, node + '/' + hash_node[0], 'algo') == 'crc32'
        expected = int(run('fdtget', '-t', 'x', source, node + '/' + hash_node[0], 'value'), 16)
        assert zlib.crc32(payloads[index]) == expected
    old_dtb = payloads[2]
    old_hash = hashlib.sha256(old_dtb).digest()
    hash_nodes = run('fdtget', '-l', source, FDT_NODE).splitlines()
    assert len(hash_nodes) == 1
    hash_node = FDT_NODE + '/' + hash_nodes[0]
    assert run('fdtget', source, hash_node, 'algo') == 'sha256'
    recorded = bytes(int(x, 16) for x in run('fdtget', '-t', 'bx', source, hash_node, 'value').split())
    assert recorded == old_hash
    board = out / 'board.dtb'
    shutil.copyfile(out / 'board-original.dtb', board)
    sd_node = '/cv-sd@4310000'
    assert run('fdtget', board, sd_node, 'compatible') == 'cvitek,cv181x-sd'
    assert run('fdtget', '-t', 'u', board, sd_node, 'max-frequency') == '25000000'
    assert run('fdtget', '-t', 'u', board, sd_node, 'bus-width') == '4'
    run('fdtget', board, sd_node, 'no-1-8-v')
    run('fdtput', '-t', 'u', board, sd_node, 'max-frequency', '50000000')
    new_dtb = board.read_bytes()
    assert len(new_dtb) == len(old_dtb)
    # Restore the old frequency using libfdt and require byte-exact reversal.
    reverse = out / 'board-reversed.dtb'
    shutil.copyfile(board, reverse)
    run('fdtput', '-t', 'u', reverse, sd_node, 'max-frequency', '25000000')
    assert reverse.read_bytes() == old_dtb
    changes = [i for i, (a, b) in enumerate(zip(old_dtb, new_dtb)) if a != b]
    start = changes[0]
    assert old_dtb[start:start + 4] == struct.pack('>I', 25000000)
    assert new_dtb[start:start + 4] == struct.pack('>I', 50000000)
    assert all(start <= i < start + 4 for i in changes)
    dt_offset = unique_offset(original, old_dtb)
    hash_offset = unique_offset(original, old_hash)
    new_hash = hashlib.sha256(new_dtb).digest()
    candidate = bytearray(original)
    candidate[dt_offset:dt_offset + len(old_dtb)] = new_dtb
    candidate[hash_offset:hash_offset + 32] = new_hash
    fit = out / 'boot-sd50.diagnostic'
    fit.write_bytes(candidate)
    # Original metadata/layout and every unrelated byte are retained.
    reversed_fit = bytearray(candidate)
    reversed_fit[dt_offset:dt_offset + len(old_dtb)] = old_dtb
    reversed_fit[hash_offset:hash_offset + 32] = old_hash
    assert reversed_fit == original
    for index, expected in enumerate([*payloads[:2], new_dtb]):
        extracted = out / ('roundtrip-' + str(index))
        run(dumpimage, '-T', 'flat_dt', '-p', index, '-o', extracted, fit)
        assert extracted.read_bytes() == expected
    recorded = bytes(int(x, 16) for x in run('fdtget', '-t', 'bx', fit, hash_node, 'value').split())
    assert recorded == new_hash
    (out / 'legacy-reader.txt').write_text(run(dumpimage, '-l', fit) + '\n')
    report = {
        'purpose': 'SD-only diagnostic on current RC4 kernel; not Enhanced release',
        'source_sha256': SOURCE_SHA,
        'candidate_sha256': hashlib.sha256(candidate).hexdigest(),
        'bytes': len(candidate),
        'sd_node': sd_node,
        'max_frequency_hz': 50000000,
        'no_1_8_v': True,
        'kernel_ramdisk_unchanged': True,
        'dt_byte_exact_reverse_check': True,
        'fit_byte_exact_reverse_check': True,
        'loader_extraction_and_payload_hashes': 'PASS',
        'installed': False,
        'actual_frequency_and_io_qualification': 'PENDING',
    }
    (out / 'manifest.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
