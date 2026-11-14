#!/usr/bin/env python3
"""Reuse verified component outputs; packaging, source and signing remain fresh."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import stat
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = 1
# paths, upstream pins, dependencies, outputs consumed by later steps.
COMPONENTS = {
    'kernel': (['platform/kernel'], ['linux'], [], ['kernel', 'images/Image']),
    'modules': (['platform/modules'], ['osdrv', 'aic8800', 'sipeed-sdk'], ['kernel'],
                ['modules/sources/osdrv', 'images/lib']),
    'uboot': (['platform/uboot'], ['u-boot'], [], ['images/u-boot.bin', 'uboot/src/Licenses']),
    'opensbi': (['platform/opensbi', 'scripts/nanokvm-opensbi-manifest.py'], ['opensbi'], [],
                ['images/opensbi', 'opensbi/src/COPYING.BSD']),
    'fip': (['platform/fsbl', 'scripts/nanokvm-fip.py', 'scripts/gen-oled-boot-logo.py',
             'support/kvm_system/main/lib/oled_ctrl/oled_font.h'],
            ['sipeed-sdk'], ['uboot', 'opensbi'],
            ['images/fip.bin', 'images/fip-manifest.json', 'images/fsbl',
             'images/oled-logo', 'images/fip-licenses']),
    'initramfs': (['platform/boot'], [], ['kernel'],
                  ['images/initramfs.cpio.lz4', 'images/licenses/initramfs']),
    'boot': (['platform/boot', 'firmware/boards'], [], ['kernel', 'initramfs'],
             ['images/boot', 'images/dtb', 'images/boot-fit']),
    'native': (['platform/native', 'firmware/mpi', 'firmware/sensor', 'firmware/sources.json',
                'support/additional', 'scripts/build-mpi.sh', 'scripts/build-isp.py',
                'scripts/build-mpi-bin.py', 'scripts/build-hdmi-light.py', 'scripts/build-mmf.py',
                'scripts/build-capture.py', 'scripts/nanokvm_mmf_link.py'],
               ['cvi-mpi', 'sensors', 'json-c', 'miniz', 'inih'], ['modules'],
               ['images/native', 'images/native-licenses']),
    'tpu': (['tpu'], ['tpu-runtime'], [], ['images/tpu']),
    'system': (['support/kvm_system', 'support/additional/kvm', 'scripts/build-system.py'],
               [], [], ['images/system']),
    'tools': (['support/nanokvm_update_edid', 'firmware/boards', 'firmware/probes/edid120',
               'firmware/services/nanokvm-serial-console.sh', 'firmware/crypto/openssl-c906',
               'firmware/cpu/go-crypto-c906', 'platform/lolcat',
               'firmware/alpine/packages/nanokvm-zlib/zlib-1.3.2.symbols',
               'server/service/stream/audio/capture',
               'scripts/build-busybox-devmem.sh', 'scripts/build-serial-console-disk.py',
               'scripts/build-qhd-edid.py', 'scripts/build-monitor-edids.py',
               'scripts/build-portrait-edid.py', 'scripts/build-wide-monitor-edids.py',
               'scripts/edid-rgb-range.py', 'scripts/build-usb-audio.py', 'scripts/build-openssl.py'],
              ['busybox', 'exfatprogs', 'i2c-tools', 'lolcat', 'tinyalsa', 'opus', 'openssl', 'zlib-ng'],
              ['kernel'], ['images/tools', 'tools/compress']),
    'server': (['server', 'firmware/cpu', 'scripts/build-server-existing-libs.py'],
               ['go'], ['native', 'tools'], ['images/server']),
    'web': (['web', '.nvmrc'], ['node', 'pnpm'], [], ['images/web']),
    'firmware': ([], ['aic8800', 'regdb', 'sipeed-sdk'], [], ['images/firmware']),
}
COMMON = ['platform/build.sh', 'platform/compiler-cache.sh', 'platform/cpu-profile.json',
          'scripts/nanokvm_cpu_profile.py', 'scripts/build-components.py']


def tree_digest(root, names, source=False):
    """Hash content, names, modes and links; mtimes do not affect build output."""
    digest = hashlib.sha256()

    def visit(path, relative):
        if source and (path.name in ('.git', '__pycache__') or
                       relative in ('web/node_modules', 'web/dist')):
            return
        info = path.lstat()
        digest.update(json.dumps([relative, stat.S_IMODE(info.st_mode),
                                  stat.S_IFMT(info.st_mode)], separators=(',', ':')).encode() + b'\0')
        if stat.S_ISLNK(info.st_mode):
            digest.update(os.fsencode(os.readlink(path)) + b'\0')
            if source:
                target = path.resolve(strict=True)
                if not target.is_relative_to(root) or not target.is_file():
                    raise ValueError('Source link must name a file inside the repository: ' + relative)
                visit(target, relative + '->' + target.relative_to(root).as_posix())
        elif stat.S_ISDIR(info.st_mode):
            for child in sorted(path.iterdir()):
                visit(child, relative + '/' + child.name)
        elif stat.S_ISREG(info.st_mode):
            digest.update(str(info.st_size).encode() + b'\0')
            with path.open('rb') as file:
                while chunk := file.read(1024 * 1024):
                    digest.update(chunk)
        else:
            raise ValueError('Unsupported component file: ' + str(path))
    for name in sorted(names):
        visit(root / name, name)
    return digest.hexdigest()


def hashed(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def fingerprints(root, out):
    lock = {line.split()[0]: line.split()[1:] for line in
            (root / 'platform/sources.lock').read_text().splitlines()
            if line.strip() and not line.lstrip().startswith('#')}
    toolchain = subprocess.check_output([str(root / 'platform/build.sh'), '-o', str(out),
                                        'toolchain-id'], cwd=root, text=True).strip()
    if len(toolchain) != 64 or any(c not in '0123456789abcdef' for c in toolchain):
        raise ValueError('Invalid toolchain identity')
    context = {'root': str(root), 'output': str(out),
               'kernel_profile': os.environ.get('NANOKVM_KERNEL_PROFILE', 'release'),
               'patch_git': os.environ.get('NANOKVM_PATCH_GIT', '0'),
               'host': [platform.machine(), platform.libc_ver(), platform.python_version()]}
    result = {}
    for name, (paths, pins, dependencies, outputs) in COMPONENTS.items():
        # Include new and ignored source files too; only known generated
        # Python metadata and the web dependency/output trees are excluded.
        source_hash = tree_digest(root, sorted(set(COMMON + paths)), source=True)
        result[name] = hashed({'schema': SCHEMA, 'source': source_hash,
                               'pins': {pin: lock[pin] for pin in pins},
                               'dependencies': {d: result[d] for d in dependencies},
                               'toolchain': None if name in ('web', 'firmware') else toolchain,
                               'context': context, 'outputs': outputs})
    return result


def load_state(path):
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text())
        if data['schema'] == SCHEMA and isinstance(data['components'], dict):
            return data['components']
    except (ValueError, KeyError, TypeError):
        pass
    print('Component metadata is incompatible; rebuilding components', flush=True)
    return {}


def save_state(path, records):
    temporary = path.with_suffix('.tmp')
    temporary.write_text(json.dumps({'schema': SCHEMA, 'components': records}, sort_keys=True) + '\n')
    temporary.replace(path)


def build(root, out, steps, verify=False):
    inputs = fingerprints(root, out)
    path = out / 'component-state.json'
    records = load_state(path)
    wanted = set(steps)

    def require(name):
        for dep in COMPONENTS[name][2]:
            if dep not in wanted:
                wanted.add(dep)
                require(dep)
    for name in list(wanted):
        require(name)
    ready = {}
    built, reused = [], []
    for name, (_, _, dependencies, outputs) in COMPONENTS.items():
        if name not in wanted:
            continue
        expected = hashed({'input': inputs[name], 'dependencies': {d: ready[d] for d in dependencies}})
        previous = records.get(name, {})
        try:
            actual = tree_digest(out, outputs)
        except FileNotFoundError:
            actual = None
        if previous.get('input') == expected and actual and previous.get('output') == actual:
            print(f'REUSE {name}: {actual[:12]}', flush=True)
            reused.append(name)
        else:
            if verify:
                raise ValueError('Component requires rebuilding: ' + name)
            records.pop(name, None)
            save_state(path, records)  # Interrupted builds cannot retain a valid stamp.
            print(f'BUILD {name}', flush=True)
            subprocess.run([str(root / 'platform/build.sh'), '-o', str(out), name], cwd=root,
                           env=dict(os.environ, NANOKVM_PATCH_GIT=os.environ.get('NANOKVM_PATCH_GIT', '0')),
                           check=True)
            actual = tree_digest(out, outputs)
            if fingerprints(root, out)[name] != inputs[name]:
                raise ValueError('Build inputs changed while building ' + name)
            records[name] = {'input': expected, 'output': actual}
            save_state(path, records)
            built.append(name)
        ready[name] = actual
    print('Components: built=' + ','.join(built) + '; reused=' + ','.join(reused), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--key', action='store_true', help='print the Actions snapshot identity')
    parser.add_argument('--verify', action='store_true', help='verify reuse without building')
    parser.add_argument('steps', nargs='*', choices=list(COMPONENTS))
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    if args.key:
        print(hashed(fingerprints(ROOT, out)))
    else:
        build(ROOT, out, args.steps or list(COMPONENTS), args.verify)
        print(f'Component processing: {time.monotonic() - started:.1f}s', flush=True)


if __name__ == '__main__':
    main()
