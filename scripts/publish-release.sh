#!/usr/bin/env bash
# Publish public OS assets only; the workflow stages packages for Pages.
set -euo pipefail
exec python3 "$(dirname "$0")/publish-release.py" "$@"
