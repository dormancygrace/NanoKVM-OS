#!/usr/bin/env python3
"""Assemble the Proxmox VE Helper-Scripts version of the serial console helper:
the bash wrapper template with the POSIX helper embedded in a quoted heredoc,
so the published script runs without any download. --check verifies that the
committed output matches."""
import argparse
from pathlib import Path
import sys

repo = Path(__file__).resolve().parents[1]
helper = repo / 'firmware/services/nanokvm-serial-console.sh'
template = repo / 'firmware/services/community-scripts/nanokvm-serial-console.sh.in'
output = repo / 'firmware/services/community-scripts/nanokvm-serial-console.sh'
DELIMITER = 'NANOKVM_SERIAL_CONSOLE_HELPER'


def build():
    body = helper.read_text()
    assert DELIMITER not in body, 'the helper must not contain the heredoc delimiter'
    text = template.read_text()
    assert text.count('__HELPER__\n') == 1
    return text.replace('__HELPER__\n', body if body.endswith('\n') else body + '\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--check', action='store_true')
    a = p.parse_args()
    built = build()
    if a.check:
        if output.read_text() != built:
            sys.exit(f'{output} is out of date: run {Path(__file__).name}')
        print('community script up to date')
        return
    output.write_text(built)
    output.chmod(0o755)
    print(f'wrote {output} ({len(built.splitlines())} lines)')


if __name__ == '__main__':
    main()
