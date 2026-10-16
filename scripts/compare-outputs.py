#!/usr/bin/env python3
"""Compare two SHA256SUMS files of platform/build.sh outputs.

The hashes step of build.sh writes OUTPUT/images/SHA256SUMS. CI publishes it
for every build of main, and comments on a pull request with the outputs that
the pull request changes. Locally, compare two builds of one tree to check
that the build is reproducible (--check fails on any difference).
"""

import argparse
from collections import defaultdict
from pathlib import Path
import re
import sys

# Outputs that change for a reason other than their own sources.
NOTES = {
    'opensbi/build-manifest.json': 'records the hash of platform/build.sh',
}
# Groups longer than this are summarised by count.
LIST_LIMIT = 12


def read(path):
    sums = {}
    for number, line in enumerate(Path(path).read_text().splitlines(), 1):
        if not line.strip() or line.startswith('#'):
            continue
        # sha256sum writes "HASH  NAME", or "HASH *NAME" in binary mode.
        m = re.fullmatch(r'([0-9a-f]{64}) [ *](.+)', line)
        if not m:
            sys.exit(f'{path}:{number}: not a sha256sum line')
        sums[m.group(2).removeprefix('./')] = m.group(1)
    return sums


def compare(old, new):
    changed = sorted(n for n in old.keys() & new.keys() if old[n] != new[n])
    added = sorted(new.keys() - old.keys())
    removed = sorted(old.keys() - new.keys())
    return changed, added, removed


def group(names):
    groups = defaultdict(list)
    for name in names:
        groups[name.split('/', 1)[0] if '/' in name else '.'].append(name)
    return dict(sorted(groups.items()))


def entry(name):
    note = NOTES.get(name)
    return f'{name} ({note})' if note else name


def text_report(changed, added, removed, total):
    if not (changed or added or removed):
        return f'All {total} outputs are identical.\n'
    lines = [f'{len(changed)} changed, {len(added)} added, {len(removed)} removed of {total} outputs.']
    for sign, names in (('~', changed), ('+', added), ('-', removed)):
        for top, members in group(names).items():
            if len(members) > LIST_LIMIT:
                lines.append(f'  {sign} {top}/: {len(members)} files')
            else:
                lines.extend(f'  {sign} {entry(n)}' for n in members)
    return '\n'.join(lines) + '\n'


def markdown_report(changed, added, removed, total, title, base):
    out = [f'### {title}', '']
    if base:
        out += [f'Compared with {base}.', '']
    if not (changed or added or removed):
        return '\n'.join(out + [f'All {total} outputs are identical.', ''])
    out += [f'**{len(changed)} changed, {len(added)} added, {len(removed)} removed** of {total} outputs.', '']
    out += ['| | directory | files |', '| --- | --- | --- |']
    for sign, names in (('changed', changed), ('added', added), ('removed', removed)):
        for top, members in group(names).items():
            if len(members) > LIST_LIMIT:
                listed = f'{len(members)} files'
            else:
                listed = '<br>'.join(f'`{n}`' + (f' ({NOTES[n]})' if n in NOTES else '') for n in members)
            out.append(f'| {sign} | `{top}` | {listed} |')
    return '\n'.join(out + [''])


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument('old', help='SHA256SUMS of the reference build')
    p.add_argument('new', help='SHA256SUMS of the build to compare')
    p.add_argument('--markdown', action='store_true', help='write a Markdown report (for a PR comment)')
    p.add_argument('--title', default='Build outputs', help='heading of the Markdown report')
    p.add_argument('--base', help='what the reference build is, for the Markdown report')
    p.add_argument('--check', action='store_true', help='exit 1 if the outputs differ')
    a = p.parse_args()
    old, new = read(a.old), read(a.new)
    changed, added, removed = compare(old, new)
    if a.markdown:
        sys.stdout.write(markdown_report(changed, added, removed, len(new), a.title, a.base))
    else:
        sys.stdout.write(text_report(changed, added, removed, len(new)))
    return 1 if a.check and (changed or added or removed) else 0


if __name__ == '__main__':
    sys.exit(main())
