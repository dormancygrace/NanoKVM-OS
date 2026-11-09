#!/usr/bin/env python3
"""Pull request test budget (AGENTS.md, "Tests: delete by default").

Added test lines must stay within a third of added code lines, unless the pull
request description has a line "Test-budget: <reason>".

  check-test-budget.py BASE_REF HEAD_REF   (description read from $GITHUB_EVENT_PATH)
"""
import json
import os
import re
import subprocess
import sys

TEST = re.compile(r'(^|/)(tests?|testdata|e2e|linux-test)/|_test\.go$|\.test\.tsx?$|\.spec\.tsx?$'
                  r'|(^|/)(test|verify|bench)[-_][^/]*\.(py|c|S|sh|go)$|\.jsonl$')
IGNORED = re.compile(r'(^|/)third_party/|\.(md|json|lock|svg|png|csv|txt)$|^\.github/')

base, head = sys.argv[1], sys.argv[2]
out = subprocess.run(['git', 'diff', '--numstat', f'{base}...{head}'],
                     capture_output=True, text=True, check=True).stdout
tests = code = 0
for line in out.splitlines():
    added, _, path = line.split('\t', 2)
    if added == '-':
        continue
    if TEST.search(path):
        tests += int(added)
    elif not IGNORED.search(path):
        code += int(added)

body = ''
event = os.environ.get('GITHUB_EVENT_PATH')
if event and os.path.exists(event):
    with open(event) as f:
        body = (json.load(f).get('pull_request') or {}).get('body') or ''

print(f'added code lines {code}, added test lines {tests}, budget {code // 3}')
if tests > code // 3:
    reason = re.search(r'^Test-budget:\s*\S.*$', body, re.M)
    if reason:
        print(f'over budget, accepted: {reason.group(0)}')
    else:
        sys.exit('Test lines exceed a third of the code lines. Delete tests (AGENTS.md), '
                 'or add a "Test-budget: <reason>" line to the pull request description.')
