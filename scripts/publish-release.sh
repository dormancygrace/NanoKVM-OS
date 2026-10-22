#!/usr/bin/env bash
# Publish split release assets and dispatch the Pages repository workflow.
set -euo pipefail
exec python3 "$(dirname "$0")/publish-release.py"
