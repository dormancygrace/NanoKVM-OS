#!/bin/sh
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/../../.." && pwd)
S95="$ROOT/kvmapp/system/init.d/S95nanokvm"
BASE="$ROOT/firmware/alpine/packages/nanokvm-base"

sh -n "$S95"
sh -n "$BASE/nanokvm-banner.sh"
sh -n "$BASE/nanokvm-base.post-install"
sh -n "$BASE/nanokvm-base.post-upgrade"

if grep -Fq '/root/.profile' "$S95"; then
    echo 'application init still owns the root login profile' >&2
    exit 1
fi

legacy='export PS1="\u@\h:\w# "'
for hook in "$BASE/nanokvm-base.post-install" "$BASE/nanokvm-base.post-upgrade"; do
    grep -Fq "legacy_root_profile='$legacy'" "$hook"
    # Compare the literal guard in the package hook.
    # shellcheck disable=SC2016
    grep -Fq '[ "$(cat /root/.profile)" = "$legacy_root_profile" ]' "$hook"
    grep -Fq 'rm -f /root/.profile' "$hook"
    grep -Fq "tmpfs_entry='tmpfs /tmp tmpfs rw,nosuid,nodev,mode=1777,size=64M 0 0'" "$hook"
done

# The next two checks look for literal assignments in the banner.
# shellcheck disable=SC2016
grep -Fq '_nanokvm_prompt_user=$nanokvm_banner_user' "$BASE/nanokvm-banner.sh"
# shellcheck disable=SC2016
grep -Fq '_nanokvm_prompt_host=$nanokvm_banner_accent' "$BASE/nanokvm-banner.sh"

# Banner selection is a fixed, non-evaluated value. Missing or unknown values
# keep the package-owned default, while rainbow retains its reviewed artwork.
grep -Fq '/etc/kvm/banner-style' "$BASE/nanokvm-banner.sh"
grep -Fq '[ "$nanokvm_banner_selected_style" = rainbow ]' "$BASE/nanokvm-banner.sh"
grep -Fq "[ \"\$nanokvm_banner_style\" = rainbow ]" "$BASE/nanokvm-banner.sh"
grep -Fq 'NN    N                         KK   KK VV   VV MM   MM' "$BASE/nanokvm-banner.sh"
grep -Fq '::::    :::     :::     ::::    :::  ::::::::  :::    ::: :::     ::: ::::     ::::' "$BASE/nanokvm-banner.sh"
grep -Fq '#+#   #+#+# #+#     #+# #+#   #+#+# #+#    #+# #+#   #+#    #+# #+#   #+#' "$BASE/nanokvm-banner.sh"

# Exercise the actual rendering branch without touching the host's /etc/kvm.
# The generated copy bypasses only the interactive-login guards and redirects
# the fixed style path into this test's private temporary directory.
banner_test_dir=$(mktemp -d)
trap 'rm -rf "$banner_test_dir"' EXIT HUP INT TERM
banner_test_script="$banner_test_dir/nanokvm-banner.sh"
sed \
    -e '/^case \$- in$/,/^esac$/c\:' \
    -e '/^\[ -n "\${SSH_CONNECTION:-}" \] || return 0$/c\:' \
    -e '/^\[ -t 1 \] || return 0$/c\:' \
    -e "s|/etc/kvm/banner-style|$banner_test_dir/banner-style|g" \
    "$BASE/nanokvm-banner.sh" > "$banner_test_script"

render_banner() {
    TERM=dumb sh "$banner_test_script"
}

default_output=$(render_banner)
printf '%s\n' "$default_output" | grep -Fq 'NN    N                         KK   KK VV   VV MM   MM'
printf '%s\n' "$default_output" | grep -Fq 'NanoKVM OS unknown'
if printf '%s\n' "$default_output" | grep -Fq '::::    :::'; then
    echo 'missing style override rendered the rainbow banner' >&2
    exit 1
fi

printf '%s\n' rainbow > "$banner_test_dir/banner-style"
rainbow_output=$(render_banner)
printf '%s\n' "$rainbow_output" | grep -Fq '::::    :::     :::     ::::    :::'
printf '%s\n' "$rainbow_output" | grep -Fq 'NanoKVM OS unknown'
if printf '%s\n' "$rainbow_output" | grep -Fq 'NN    N                         KK   KK VV   VV MM   MM'; then
    echo 'rainbow override rendered the default banner' >&2
    exit 1
fi

printf '%s\n' unknown > "$banner_test_dir/banner-style"
unknown_output=$(render_banner)
printf '%s\n' "$unknown_output" | grep -Fq 'NN    N                         KK   KK VV   VV MM   MM'
if printf '%s\n' "$unknown_output" | grep -Fq '::::    :::'; then
    echo 'unknown style rendered the rainbow banner' >&2
    exit 1
fi

echo 'ssh prompt tests passed'
