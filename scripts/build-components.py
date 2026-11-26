#!/usr/bin/env python3
"""Reuse verified component outputs; packaging, source and signing remain fresh."""
import argparse
import codecs
from importlib.util import source_from_cache
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import stat
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
SCHEMA = 3
# paths, upstream pins, dependencies, outputs consumed by later steps.
COMPONENTS = {
    'kernel': (['platform/kernel'], ['linux'], [], ['images/Image', 'component-support/kernel']),
    'modules': (['platform/modules', 'scripts/build-c906l-firmware.py'],
                ['osdrv', 'aic8800', 'sipeed-sdk', 'opus'], ['kernel'],
                ['images/lib']),
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
               'firmware/mpi/nanokvm_i2c.h', 'firmware/mpi/nanokvm_receiver_lock.h',
               'firmware/mpi/nanokvm_receiver_timing.h',
               'firmware/services/nanokvm-serial-console.sh', 'firmware/crypto/openssl-c906',
               'firmware/cpu/go-crypto-c906', 'platform/lolcat', 'platform/zlib-ng',
               'firmware/alpine/packages/nanokvm-zlib/zlib-1.3.2.symbols',
               'support/usb-audio-capture', 'platform/modules/c906l/nanokvm_opus.h',
               'scripts/build-busybox-devmem.sh', 'scripts/build-serial-console-disk.py',
               'scripts/build-qhd-edid.py', 'scripts/build-monitor-edids.py',
               'scripts/build-portrait-edid.py', 'scripts/build-wide-monitor-edids.py',
               'scripts/edid-rgb-range.py', 'scripts/build-usb-audio.py', 'scripts/build-openssl.py'],
              ['busybox', 'exfatprogs', 'lolcat', 'tinyalsa', 'openssl', 'zlib-ng'],
              ['kernel'], ['images/tools', 'component-support/tools']),
    'server': (['server-cpp'],
               ['libwebsockets', 'glaze', 'rapidyaml', 'libdatachannel', 'libjuice', 'libsrtp', 'usrsctp',
                'plog', 'libjpeg-turbo', 'libspng', 'live555', 'curl'],
               ['native', 'tools'], ['images/server']),
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


def declared_inputs(name):
    paths, pins, dependencies, _ = COMPONENTS[name]
    result_paths, result_pins = set(COMMON + paths), set(pins)
    if name not in ('web', 'firmware'):
        # toolchain-id fingerprints this entire tree and the Buildroot pin.
        result_paths.add('firmware/buildroot')
        result_pins.add('buildroot')
    for dependency in dependencies:
        more_paths, more_pins = declared_inputs(dependency)
        result_paths.update(more_paths)
        result_pins.update(more_pins)
    return result_paths, result_pins


def check_input_coverage(functions, lock):
    """Reject a new build.sh input before it can bypass cache invalidation."""
    starts = list(re.finditer(r'(?m)^([a-zA-Z_][a-zA-Z_0-9]*) \(\) ', functions))
    bodies = {m[1]: functions[m.end():starts[i+1].start() if i+1 < len(starts) else len(functions)]
              for i, m in enumerate(starts)}
    reference = re.compile(r'\$(?:\{(repo|here)\}|(repo|here))/([a-zA-Z0-9_./+-]*)')
    call = re.compile(r'(?:^\s*|[;(&|]\s*)([a-zA-Z_][a-zA-Z_0-9]*)\b', re.M)
    archives = {row[0].rsplit('/', 1)[-1]: pin for pin, row in lock.items()}
    archives.update({pin + '.tar': pin for pin, row in lock.items() if row[0].endswith('.git')})
    for component in COMPONENTS:
        paths, pins = declared_inputs(component)
        pending = [component] + (['kernel_state'] if component == 'kernel' else [])
        visited = set()
        while pending:
            name = pending.pop()
            if name in visited:
                continue
            visited.add(name)
            body = bodies[name]
            for ref in reference.finditer(body):
                path = ('platform/' if (ref[1] or ref[2]) == 'here' else '') + ref[3].rstrip('/')
                if name == 'lock' and path == 'platform/sources.lock':
                    continue  # Individual rows are fingerprinted and checked below.
                if not any(path == p or path.startswith(p + '/') for p in paths):
                    raise ValueError(f'{component}: undeclared repository input {path} in {name}')
            # Literal upstream references, including fixed archive names.
            loops = {}
            for variable, values in re.findall(r'for\s+(\w+)\s+in\s+([^;]+);', body):
                loops[variable] = [] if variable in loops else shlex.split(values)
            used = set()
            for argument in re.findall(r'\b(?:lock|unpack_git)\s+("[^"]*"|\'[^\']*\'|[^\s;()]+)', body):
                argument = shlex.split(argument)[0]
                if argument.startswith('$'):
                    variable = argument.removeprefix('$').strip('{}')
                    values = loops.get(variable, [])
                    if not values or any(not re.fullmatch('[a-zA-Z0-9_-]+', v) for v in values):
                        raise ValueError(f'{component}: unresolved upstream input {argument} in {name}')
                    used.update(values)
                else:
                    used.add(argument)
            for archive in re.findall(r'\$dl/([a-zA-Z0-9_.+-]+)', body):
                if archive == 'pnpm-store':
                    continue  # Package-manager caches are covered by their lockfiles.
                if archive not in archives:
                    raise ValueError(f'{component}: unpinned archive {archive}')
                used.add(archives[archive])
            if not used <= pins:
                raise ValueError(f'{component}: undeclared upstream inputs {sorted(used - pins)}')
            pending.extend(c for c in call.findall(body) if c in bodies and c not in visited)


def run_checked(root, out, name, command, env):
    """Audit real helper/compiler reads before accepting a component stamp."""
    paths, _ = declared_inputs(name)
    opened = set()
    with tempfile.TemporaryDirectory(prefix='component-inputs-', dir=out) as temporary:
        trace = Path(temporary) / 'files'
        # -yy resolves returned file descriptors, including relative openat
        # calls made after chdir or through a directory fd in a child process.
        subprocess.run(['strace', '--seccomp-bpf', '-f', '-qq', '-yy', '-s', '0',
                        '-e', 'trace=open,openat,openat2', '-e', 'status=successful',
                        '-o', str(trace),
                        *command], cwd=root, env=env, check=True)
        with trace.open() as log:
            for line in log:
                match = re.search(r' = [0-9]+<(/.*)>$', line.rstrip())
                if not match:
                    continue
                flags = re.search(r'"(?:[^"\\]|\\.)*", (?:\{flags=)?(O_[A-Z0-9_|]+)', line)
                if flags and 'O_WRONLY' in flags[1].split('|'):
                    continue  # Python's atomic .pyc temporary files are outputs.
                value = codecs.escape_decode(match[1].encode())[0]
                path = Path(os.fsdecode(value).removesuffix(' (deleted)'))
                if path.is_relative_to(out) or not path.is_relative_to(root) or path.is_dir():
                    continue
                relative = path.relative_to(root)
                if '.git' in relative.parts:
                    continue  # Build metadata is not a compiled source input.
                if '__pycache__' in relative.parts:
                    # An existing .pyc must not hide an undeclared Python import.
                    relative = Path(source_from_cache(str(relative)))
                relative = relative.as_posix()
                if relative == 'platform/sources.lock':
                    continue  # Individual pins are checked/fingerprinted above.
                opened.add(relative)
    unknown = sorted(p for p in opened if not any(p == item or p.startswith(item + '/')
                                                for item in paths))
    if unknown:
        raise ValueError(f'{name}: undeclared repository reads: ' + ', '.join(unknown))
    print(f'{name}: audited {len(opened)} repository inputs', flush=True)


class Inputs:
    def __init__(self, root, out):
        self.root = root
        self.lock = {line.split()[0]: line.split()[1:] for line in
                     (root / 'platform/sources.lock').read_text().splitlines()
                     if line.strip() and not line.lstrip().startswith('#')}
        toolchain = subprocess.check_output([str(root / 'platform/build.sh'), '-o', str(out),
                                            'toolchain-id'], cwd=root, text=True).strip()
        if not re.fullmatch('[0-9a-f]{64}', toolchain):
            raise ValueError('Invalid toolchain identity')
        functions = subprocess.check_output([str(root / 'platform/build.sh'), '-o', str(out),
                                            'component-functions'], cwd=root, text=True)
        check_input_coverage(functions, self.lock)
        # Explicit output-affecting flags only. Kernel/profile options are
        # normalized below; helper source/tool/output locations are supplied by
        # build.sh. Cache controls and release metadata do not change binaries.
        compiler_env = {'CC', 'CXX', 'AR', 'LD', 'CFLAGS', 'CXXFLAGS', 'CPPFLAGS',
                        'LDFLAGS', 'MAKEFLAGS', 'SOURCE_DATE_EPOCH', 'RUSTFLAGS'}
        self.context = {'root': str(root), 'output': str(out),
                        'kernel_profile': os.environ.get('NANOKVM_KERNEL_PROFILE', 'release'),
                        'patch_git': os.environ.get('NANOKVM_PATCH_GIT', '0'),
                        'environment': {k: v for k, v in os.environ.items()
                                        if k in compiler_env},
                        'host': [platform.machine(), platform.libc_ver(), platform.python_version()]}
        self.toolchain = toolchain

    def own(self, name):
        paths, pins, _, outputs = COMPONENTS[name]
        source = tree_digest(self.root, sorted(set(COMMON + paths)), source=True)
        return hashed({'schema': SCHEMA, 'source': source,
                       'pins': {pin: self.lock[pin] for pin in pins},
                       'toolchain': None if name in ('web', 'firmware') else self.toolchain,
                       'context': self.context, 'outputs': outputs})

    def all(self):
        own, result = {}, {}
        for name, (_, _, dependencies, _) in COMPONENTS.items():
            own[name] = self.own(name)
            result[name] = hashed({'own': own[name], 'dependencies': {d: result[d] for d in dependencies}})
        return own, result


def fingerprints(root, out):
    return Inputs(root, out).all()[1]


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


def image_inventory(out, prune=False):
    root = out / 'images'
    if not root.exists():
        return
    owned = {p.removeprefix('images/') for item in COMPONENTS.values()
             for p in item[3] if p.startswith('images/')}
    def visit(path):
        relative = path.relative_to(root).as_posix()
        if relative in owned:
            return
        if any(p.startswith(relative + '/') for p in owned) and path.is_dir() and not path.is_symlink():
            for child in path.iterdir():
                visit(child)
        elif prune:
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink()
            print('PRUNE images/' + relative, flush=True)
        else:
            raise ValueError('Undeclared image output: ' + relative)
    for path in root.iterdir():
        visit(path)


def build(root, out, steps, verify=False):
    source = Inputs(root, out)
    own, inputs = source.all()
    image_inventory(out, prune=not verify)
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
    kernel_work = osdrv_work = False
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
            if name in ('modules', 'initramfs', 'boot', 'native', 'tools') and not kernel_work:
                run_checked(root, out, name,
                            [str(root / 'platform/build.sh'), '-o', str(out), 'kernel-state'],
                            dict(os.environ, NANOKVM_PATCH_GIT='0'))
                kernel_work = True
            if name == 'native' and not osdrv_work:
                run_checked(root, out, name,
                            [str(root / 'platform/build.sh'), '-o', str(out), 'osdrv-source'],
                            dict(os.environ, NANOKVM_PATCH_GIT='0'))
                osdrv_work = True
            # A changed producer gets a clean destination, including producers
            # that overwrite a file without clearing its containing directory.
            for output in outputs:
                target = out / output
                if target.is_dir() and not target.is_symlink():
                    shutil.rmtree(target)
                elif target.exists() or target.is_symlink():
                    target.unlink()
            print(f'BUILD {name}', flush=True)
            run_checked(root, out, name,
                        [str(root / 'platform/build.sh'), '-o', str(out), name],
                        dict(os.environ, NANOKVM_PATCH_GIT=os.environ.get('NANOKVM_PATCH_GIT', '0')))
            if name == 'kernel':
                kernel_work = True
            elif name == 'modules':
                osdrv_work = True
            actual = tree_digest(out, outputs)
            if source.own(name) != own[name]:
                raise ValueError('Build inputs changed while building ' + name)
            records[name] = {'input': expected, 'output': actual}
            save_state(path, records)
            built.append(name)
        ready[name] = actual
    image_inventory(out)
    print('Components: built=' + ','.join(built) + '; reused=' + ','.join(reused), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--key', action='store_true', help='print the Actions snapshot identity')
    parser.add_argument('--check-inputs', action='store_true')
    parser.add_argument('--verify', action='store_true', help='verify reuse without building')
    parser.add_argument('steps', nargs='*', choices=list(COMPONENTS))
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    if args.check_inputs:
        Inputs(ROOT, out)
        print("Component input coverage checked")
    elif args.key:
        print(hashed(fingerprints(ROOT, out)))
    else:
        build(ROOT, out, args.steps or list(COMPONENTS), args.verify)
        print(f'Component processing: {time.monotonic() - started:.1f}s', flush=True)


if __name__ == '__main__':
    main()
