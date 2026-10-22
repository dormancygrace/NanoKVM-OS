#!/usr/bin/env python3
"""The package revision of an add-on's next release.

Reads the tag names of this repository on standard input, one per line, and
prints the next revision of the add-on NAME (scripts/addon_packages.py ADDONS)
at VERSION: one more than the highest NAME-VERSION-rN tag, or 0 for a version
that has none. For nanokvm-rustdesk 0.5.3, this repository already has r6
after the archived NanoKVM-OS-packages repository's r5. New source versions
start at zero; retired versions from the archive are not republished.
"""
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import addon_packages  # noqa: E402


def next_revision(name, version, tags):
    revisions = [int(m[3]) for tag in tags
                 if (m := addon_packages.TAG.fullmatch(tag.strip())) and m[1] == name and m[2] == version]
    return max(revisions) + 1 if revisions else 0


def main():
    if (len(sys.argv) != 3 or sys.argv[1] not in addon_packages.ADDONS
            or not re.fullmatch(r'\d+\.\d+\.\d+', sys.argv[2])):
        sys.exit('usage: next-addon-revision.py NAME VERSION < tags')
    print(next_revision(sys.argv[1], sys.argv[2], sys.stdin.read().split()))


if __name__ == '__main__':
    main()
