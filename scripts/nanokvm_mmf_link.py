"""Compact linkage of the existing LT6911 MMF source set.

Open MPI is linked from archives and localized. Vendor GCC10 ISP algorithms
remain shared: their legacy vector attributes cannot be merged with GCC16
xtheadvector objects. No ISA attributes or algorithm implementations are changed.
"""
from pathlib import Path
import hashlib
import json
import re
import subprocess

VENDOR_LIBRARIES = ('ae', 'awb', 'af', 'isp_algo')
OPEN_LIBRARIES = ('sys', 'vi', 'vpss', 'vo', 'rgn', 'gdc', 'venc', 'vdec',
                  'misc', 'isp', 'cvi_bin', 'cvi_bin_isp')
PROBE_EXPORTS = {
    'nanokvm_read_capture_size', 'ini_parse_string', 'g_pastLt6911',
    'stSnsLT6911_Obj', 'SAMPLE_COMM_VI_ParseIni',
    'SAMPLE_COMM_VI_GetSizeBySensor', 'SAMPLE_COMM_SYS_GetPicSize',
}


def symbols(cross, paths, *, dynamic=False, undefined=False):
    """Return external ELF symbol names and types, including weak definitions."""
    args = [cross + 'nm', '--format=posix', '--extern-only',
            '--undefined-only' if undefined else '--defined-only']
    if dynamic:
        args.append('--dynamic')
    output = subprocess.check_output([*args, *map(str, paths)], text=True)
    result = {}
    for line in output.splitlines():
        parts = line.split()
        if len(parts) >= 2 and len(parts[1]) == 1 and parts[1].isalpha():
            result[parts[0].split('@', 1)[0]] = parts[1]
    return result


def mmf_symbols(cross, obj):
    names = sorted(symbols(cross, [obj]))
    demangled = subprocess.check_output(
        [cross + 'c++filt'], input='\n'.join(names) + '\n', text=True).splitlines()
    # The established ABI contains both C++ mangled and extern "C" entry points.
    result = {name for name, display in zip(names, demangled)
              if display.startswith('mmf_')}
    if not result:
        raise ValueError('No MMF C++ API symbols found; refuse to prune the library')
    return result


def needed(cross, path):
    dynamic = subprocess.check_output([cross + 'readelf', '-d', str(path)], text=True)
    return re.findall(r'\(NEEDED\).*\[([^]]+)\]', dynamic)


def link_hdmi_light(cross, mpi, objects, shared, output):
    archives = [mpi / 'lib' / ('lib' + name + '.a') for name in OPEN_LIBRARIES]
    vendor = [mpi / 'lib' / ('lib' + name + '.so') for name in VENDOR_LIBRARIES]
    for path in [*archives, *vendor]:
        if not path.is_file():
            raise FileNotFoundError('Prepared MPI input is missing: ' + str(path))
    defined = set(symbols(cross, [*objects, *archives]))
    # Vendor callbacks into open ISP must remain visible after localization.
    callbacks = defined & set(symbols(cross, vendor, dynamic=True, undefined=True))
    public = mmf_symbols(cross, next(p for p in objects if p.stem == 'kvm_mmf'))
    exports = public | callbacks | PROBE_EXPORTS
    missing = exports - defined
    if missing:
        raise ValueError('Required exports missing: ' + ', '.join(sorted(missing)))
    export_file = output / 'hdmi-light.exports'
    export_file.write_text('{\n  global:\n' + ''.join('    ' + n + ';\n' for n in sorted(exports))
                           + '  local: *;\n};\n')
    subprocess.run([
        cross + 'g++', '-shared', '-Wl,-z,defs', '-Wl,-soname,libkvm_mmf.so',
        '-Wl,--gc-sections', '-Wl,--version-script=' + str(export_file),
        '-Wl,-Map,' + str(output / 'hdmi-light.map'),
        '-Wl,--start-group', *map(str, objects), *map(str, archives), '-Wl,--end-group',
        '-L' + str(mpi / 'lib'), '-Wl,--as-needed',
        *['-l' + name for name in VENDOR_LIBRARIES], '-pthread', '-lm', '-ldl',
        '-o', str(shared)], check=True)
    actual = set(symbols(cross, [shared], dynamic=True))
    if actual != exports:
        raise ValueError('Unexpected compact MMF export set: ' + repr(actual ^ exports))
    dependencies = needed(cross, shared)
    allowed = {p.name for p in vendor} | {'libc.so', 'libstdc++.so.6', 'libgcc_s.so.1'}
    if set(dependencies) - allowed:
        raise ValueError('Unexpected compact MMF dependencies: ' + repr(dependencies))
    metadata = {
        'profile': 'hdmi-light', 'hardware_qualified': False,
        'mmf_api_symbols': sorted(public), 'vendor_callback_exports': sorted(callbacks),
        'probe_exports': sorted(PROBE_EXPORTS), 'needed': dependencies,
        'inputs_sha256': {str(p.relative_to(mpi)): hashlib.sha256(p.read_bytes()).hexdigest()
                          for p in [*archives, *vendor]},
        'algorithm_changes': False, 'small_core_required': False,
    }
    (output / 'link-profile.json').write_text(json.dumps(metadata, indent=2) + '\n')


def audit_bundle(cross, bundle):
    """Check DT_NEEDED closure and strong imports against bundle + toolchain ABI."""
    resolved = {p.name: p for p in bundle.glob('*.so')}
    pending = list(resolved.values())
    for path in pending:
        for name in needed(cross, path):
            if name in resolved:
                continue
            if name not in {'libc.so', 'libstdc++.so.6', 'libgcc_s.so.1'}:
                raise ValueError('Unbundled media dependency: ' + name)
            system = Path(subprocess.check_output(
                [cross + 'gcc', '-print-file-name=' + name], text=True).strip())
            if not system.is_file():
                raise FileNotFoundError('Toolchain runtime library missing: ' + name)
            resolved[name] = system
            pending.append(system)
    defined = set(symbols(cross, list(resolved.values()), dynamic=True))
    failures = {}
    for name, path in resolved.items():
        required = {n for n, kind in symbols(cross, [path], dynamic=True, undefined=True).items()
                    if kind == 'U'}
        missing = sorted(required - defined)
        if missing:
            failures[name] = missing
    if failures:
        raise ValueError('Unresolved ELF imports: ' + json.dumps(failures))
    return {name: needed(cross, path) for name, path in resolved.items()}
