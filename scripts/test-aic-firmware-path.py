#!/usr/bin/env python3
"""Test chip-scoped AIC firmware names without loading a kernel module."""
import argparse
from pathlib import Path
import re
import subprocess
import tempfile


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source', type=Path,
                    help='Optional prepared AIC driver root for call-site verification')
parser.add_argument('--firmware-tree', type=Path,
                    help='Optional installed aic8800_sdio directory for payload verification')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
policy = repo / 'firmware/wifi/aic8800'

harness = r'''
#include <assert.h>
#include <errno.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>

enum AICWF_IC {
    PRODUCT_ID_AIC8801 = 0,
    PRODUCT_ID_AIC8800DC,
    PRODUCT_ID_AIC8800DW,
    PRODUCT_ID_AIC8800D80,
    PRODUCT_ID_AIC8800D80N,
    PRODUCT_ID_AIC8800D80WN,
    PRODUCT_ID_AIC8800D80X2
};
#include "aicbsp_firmware_path.h"

static void expect(unsigned int chipid, const char *name, const char *wanted)
{
    char path[200];
    assert(aicbsp_firmware_request_name(chipid, name, path, sizeof(path)) == 0);
    assert(strcmp(path, wanted) == 0);
}

int main(void)
{
    char path[8];
    expect(PRODUCT_ID_AIC8801, "fw_patch_table.bin",
           "aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table.bin");
    expect(PRODUCT_ID_AIC8800D80, "fw_patch_table_8800d80_u02.bin",
           "aic8800_sdio/aic8800_and_aic8800D80/fw_patch_table_8800d80_u02.bin");
    expect(PRODUCT_ID_AIC8800DC, "fw_patch_table_8800dc_u02.bin",
           "aic8800_sdio/aic8800DC/fw_patch_table_8800dc_u02.bin");
    expect(PRODUCT_ID_AIC8800D80X2, "fw_patch_table_8800d80x2_u05.bin",
           "aic8800_sdio/aic8800D80X2/fw_patch_table_8800d80x2_u05.bin");
    assert(aicbsp_firmware_request_name(99, "firmware.bin", path, sizeof(path)) == -EINVAL);
    assert(aicbsp_firmware_request_name(PRODUCT_ID_AIC8801, "../firmware.bin",
                                        path, sizeof(path)) == -EINVAL);
    assert(aicbsp_firmware_request_name(PRODUCT_ID_AIC8801, "firmware.bin",
                                        path, sizeof(path)) == -ENAMETOOLONG);
    return 0;
}
'''

with tempfile.TemporaryDirectory(prefix='nkos-aic-firmware-path-') as directory:
    source = Path(directory) / 'firmware-path.c'
    binary = Path(directory) / 'firmware-path'
    source.write_text(harness)
    subprocess.run([
        'cc', '-std=c11', '-Wall', '-Wextra', '-Werror',
        '-I', str(policy), str(source), '-o', str(binary),
    ], check=True)
    subprocess.run([str(binary)], check=True)

if args.source:
    driver = args.source.resolve()
    expected_header = (policy / 'aicbsp_firmware_path.h').read_bytes()
    actual_header = (driver / 'aic8800_bsp/aicbsp_firmware_path.h').read_bytes()
    assert actual_header == expected_header, 'prepared driver has a different firmware-path policy'
    direct = []
    for relative in ('aic8800_bsp', 'aic8800_fdrv'):
        for source in (driver / relative).glob('*.c'):
            for line_number, line in enumerate(source.read_text().splitlines(), 1):
                if re.search(r'(?<!aicbsp_)request_firmware\s*\(', line):
                    direct.append((source.relative_to(driver), line_number, line.strip()))
    assert len(direct) == 1, f'unscoped request_firmware call sites remain: {direct}'
    assert direct[0][0] == Path('aic8800_bsp/aic_bsp_driver.c'), direct

# Verify the vendored, qualified D80 payload even without a full target tree.
import hashlib
expected_d80 = 'f7fa0a1a296589568fbea1de89ab8feeb13ef2e6db6c7fafa682a34fb583a1bd'
d80_name = 'fmacfwbt_8800d80_h_u02.bin'
d80_source = repo / 'firmware/buildroot/package/aic8800-sdio-firmware/files' / d80_name
assert hashlib.sha256(d80_source.read_bytes()).hexdigest() == expected_d80

if args.firmware_tree:
    firmware = args.firmware_tree.resolve()
    assert hashlib.sha256((firmware / 'aic8800_and_aic8800D80' / d80_name).read_bytes()).hexdigest() == expected_d80, 'packaged D80 firmware is stale or incorrect'
    for relative in (
        'aic8800_and_aic8800D80/fw_patch_table.bin',
        'aic8800_and_aic8800D80/fw_patch_table_8800d80_u02.bin',
    ):
        assert (firmware / relative).is_file(), f'missing packaged firmware: {relative}'

print('AIC chip-scoped firmware path tests passed')
