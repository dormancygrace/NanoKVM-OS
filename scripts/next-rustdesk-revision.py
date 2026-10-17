#!/usr/bin/env python3
"""The package revision of the next RustDesk add-on release.

Reads the tag names of this repository on standard input, one per line, and
prints the next revision for VERSION (the crate version): one more than the
highest nanokvm-rustdesk-VERSION-rN tag, or 0 for a version that has none.
The archived NanoKVM-OS-packages repository holds 0.5.3-r0 to r5 only; this
repository has 0.5.3-r6, so its tags alone give the next revision of any
version.
"""
import re
import sys


def next_revision(version, tags):
    pattern = re.compile(r'nanokvm-rustdesk-' + re.escape(version) + r'-r(\d+)')
    revisions = [int(m[1]) for tag in tags if (m := pattern.fullmatch(tag.strip()))]
    return max(revisions) + 1 if revisions else 0


def main():
    if len(sys.argv) != 2 or not re.fullmatch(r'\d+\.\d+\.\d+', sys.argv[1]):
        sys.exit('usage: next-rustdesk-revision.py VERSION < tags')
    print(next_revision(sys.argv[1], sys.stdin.read().split()))


if __name__ == '__main__':
    main()
