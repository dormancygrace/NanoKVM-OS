#!/usr/bin/env bash
# Publish assembled assets; a retry never rewrites a published release.
set -euo pipefail

title="NanoKVM OS $NANOKVM_VERSION"
[ "$IMAGE" != true ] || title="$title · Image $NANOKVM_IMAGE_VERSION"
state=$(gh release view "$NANOKVM_VERSION" --json isDraft -q .isDraft 2>/dev/null) || state=missing
case $state in
    true)
        gh release upload "$NANOKVM_VERSION" --clobber "$OUT"/assets/* ;;
    false)
        # Publishing may have succeeded before the repository dispatch failed.
        # The public assets are immutable; just retry the dispatch below.
        ;;
    missing)
        flags=(--draft)
        case $NANOKVM_VERSION in *-*) flags+=(--prerelease --latest=false) ;; esac
        [ -z "$PREVIOUS" ] || flags+=(--notes-start-tag "$PREVIOUS")
        gh release create "$NANOKVM_VERSION" --target "$SHA" "${flags[@]}" \
            --title "$title" --generate-notes "$OUT"/assets/*
        state=true ;;
    *) echo "Unexpected release state: $state" >&2; exit 1 ;;
esac
if [ "$state" = true ] && [ "$DRAFT" != true ]; then
    gh release edit "$NANOKVM_VERSION" --draft=false
    state=false
fi
if [ "$state" = false ]; then
    gh workflow run apk-repository.yml --ref "$DEFAULT_BRANCH"
fi
