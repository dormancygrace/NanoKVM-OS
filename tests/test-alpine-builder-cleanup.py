#!/usr/bin/env python3
"""Exercise real exit traps with synthetic mountinfo; never mount or delete."""
from pathlib import Path
import json
import os
import shlex
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
s = (repo / 'scripts/build-alpine-personal-image.sh').read_text()
cleanup = s[s.index('tree_is_unmounted() {'):s.index('\nwhile [ "$#" -gt 0 ]')]

def escaped(path):
    return str(path).replace('\\', r'\134').replace(' ', r'\040').replace('\t', r'\011').replace('\n', r'\012')


def check(name, mounts=(), failures=(), final_failure=False, unreadable=False, empty=False, exit_code=0, relative=False):
    with tempfile.TemporaryDirectory(prefix='builder-cleanup-') as tmp:
        t = Path(tmp)
        build = t / 'build space\\tab\tline\nend'
        stage = build / 'root'
        parse = t / 'parse'
        for path in (stage / 'dev', stage / 'proc', stage / 'sys', parse, t / 'bin'):
            path.mkdir(parents=True, exist_ok=True)
        info = t / 'mountinfo'
        mounted_paths = [(stage / p).resolve() for p in mounts]
        lines = ['1 0 0:1 / / rw - rootfs rootfs rw\n']
        lines += [f'{i+2} 1 0:2 / {escaped(p)} rw - tmpfs tmpfs rw\n' for i, p in enumerate(mounted_paths)]
        if not unreadable:
            info.write_text('' if empty else ''.join(lines))
        log = t / 'calls'
        umount = t / 'bin/umount'
        umount.write_text(r'''#!/usr/bin/python3
import json, os, sys
from pathlib import Path
p = Path(os.environ['MOUNTINFO'])
with open(os.environ['CALLS'], 'a') as f: f.write(json.dumps(['umount', *sys.argv[1:]]) + '\n')
if sys.argv[1] in json.loads(os.environ['FAILURES']): sys.exit(32)
if not p.exists(): sys.exit(32)
encoded = sys.argv[1].replace('\\', r'\134').replace(' ', r'\040').replace('\t', r'\011').replace('\n', r'\012')
lines = p.read_text().splitlines(True)
remaining = [line for line in lines if line.split()[4] != encoded]
if len(lines) == len(remaining): sys.exit(32)
p.write_text(''.join(remaining))
''')
        umount.chmod(0o755)
        rm = t / 'bin/rm'
        rm.write_text(r'''#!/usr/bin/python3
import json, os, sys
with open(os.environ['CALLS'], 'a') as f: f.write(json.dumps(['rm', *sys.argv[1:]]) + '\n')
''')
        rm.chmod(0o755)
        func = cleanup.replace('/proc/self/mountinfo', shlex.quote(str(info)))
        build_arg = build
        if relative:
            (t / 'alias').symlink_to(build, target_is_directory=True)
            build_arg = Path('alias')
        script = 'set -eu\nPROGRAM=test\n' + '\n'.join(
            f'{k}={shlex.quote(str(v))}' for k, v in {'BUILD_TMP': build_arg, 'STAGE_ROOT': stage, 'PARSE_TMP': parse}.items()) + '\n' + func
        script += '\numount "$STAGE_ROOT/sys"\n' if final_failure else f'\nexit {exit_code}\n'
        env = dict(os.environ, PATH=str(t / 'bin') + ':' + os.environ['PATH'], MOUNTINFO=str(info),
                   CALLS=str(log), FAILURES=json.dumps([str(stage / p) for p in failures]))
        result = subprocess.run(['sh', '-c', script], cwd=t, env=env, capture_output=True, text=True, timeout=10)
        calls = [json.loads(line) for line in log.read_text().splitlines()]
        removed = [call[-1] for call in calls if call[0] == 'rm']
        blocked = unreadable or empty or any(p not in ('sys', 'proc', 'dev') or p in failures for p in mounts)
        assert (str(build) not in removed) == blocked, (name, result, calls)
        assert (result.returncode != 0) == (blocked or final_failure or exit_code != 0), (name, result)
        if blocked:
            assert 'retaining' in result.stderr
        if not (unreadable or empty):
            assert str(parse) in removed
        if exit_code and not blocked:
            assert result.returncode == exit_code
        print('PASS:', name)

check('successful cleanup', ('sys', 'proc', 'dev'))
check('failed dev unmount', ('sys', 'proc', 'dev'), ('dev',))
check('partial mounting', ('dev',))
check('already unmounted paths')
check('final unmount failure and trap retry', ('sys', 'proc', 'dev'), ('sys',), final_failure=True)
check('descendant mount', ('dev', 'dev/pts'), ('dev',))
check('unexpected package-created mount', ('run/private',))
check('mount at build tree root', ('..',))
check('missing mountinfo', unreadable=True)
check('empty mountinfo', empty=True)
check('original build failure retained', exit_code=42)
check('relative symlinked staging path', ('dev',), ('dev',), relative=True)
