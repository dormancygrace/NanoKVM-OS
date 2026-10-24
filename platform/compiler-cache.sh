#!/bin/bash
# Shared configuration for Buildroot's patched ccache and external SDK builds.
# Sourced by build.sh and CI; no cache is created until a compiler runs.
configure_compiler_cache() {
    case ${NANOKVM_CCACHE:-1} in
        1) export BR2_USE_CCACHE=1; unset CCACHE_DISABLE ;;
        0) export BR2_USE_CCACHE=0 CCACHE_DISABLE=1 ;;
        *) echo 'NANOKVM_CCACHE must be 0 or 1' >&2; return 2 ;;
    esac
    # Buildroot patches ccache to read BR_CACHE_DIR instead of CCACHE_DIR.
    # Export both so system ccache and the SDK agree. Keep paths unchanged:
    # relaxed hashing/path rewriting can alter debug information and __FILE__.
    BR_CACHE_DIR=$(realpath -m "${NANOKVM_CCACHE_DIR:-$1/ccache}")
    export BR_CACHE_DIR CCACHE_DIR="$BR_CACHE_DIR"
    export CCACHE_MAXSIZE=${NANOKVM_CCACHE_MAXSIZE:-5G}
    # The SDK wrapper supplies Buildroot's compiler identity hash; do not
    # override it with the wrapper's mtime or weaken ccache's validity checks.
}
