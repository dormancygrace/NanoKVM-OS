#!/bin/sh
# Show the NanoKVM greeting only after an interactive SSH login.  This file is
# sourced by Alpine's /etc/profile, not by sshd, so remote commands, SCP and
# SFTP retain their protocol-clean stdout.
case $- in
    *i*) ;;
    *) return 0 ;;
esac

[ -n "${SSH_CONNECTION:-}" ] || return 0
[ -t 1 ] || return 0

# /etc/nanokvm-release is supplied by nanokvm-release. Parse just the
# package-owned VERSION value rather than tying the prompt to an APK revision.
nanokvm_banner_version=$(sed -n 's/^VERSION="\([^"]*\)"$/\1/p' \
    /etc/nanokvm-release 2>/dev/null | sed -n '1p')
[ -n "$nanokvm_banner_version" ] || nanokvm_banner_version="unknown"

nanokvm_banner_accent=
nanokvm_banner_user=
nanokvm_banner_reset=
case ${TERM:-} in
    ''|dumb) ;;
    *)
        [ -n "${NO_COLOR:-}" ] || {
            nanokvm_banner_accent='\033[1;38;5;48m'
            nanokvm_banner_user='\033[1;38;5;45m'
            nanokvm_banner_reset='\033[0m'
        }
        ;;
esac

nanokvm_banner_style=default
if [ -r /etc/kvm/banner-style ]; then
    IFS= read -r nanokvm_banner_selected_style < /etc/kvm/banner-style || true
    [ "$nanokvm_banner_selected_style" = rainbow ] && nanokvm_banner_style=rainbow
fi

nanokvm_banner_print_rainbow3() {
    if [ -z "$nanokvm_banner_accent" ]; then
        while IFS= read -r nanokvm_banner_line; do
            printf '%s\n' "$nanokvm_banner_line"
        done
        return
    fi

    awk '
        BEGIN {
            split("255;40;40 255;120;0 255;180;0 255;220;0 220;255;0 120;255;0 0;255;80 0;255;160 0;200;255 0;120;255 120;80;255 255;0;200", color, " ")
        }
        {
            row = NR - 1
            pos = 1
            color_index = int(row / 2)

            if (row % 2 == 0) {
                printf "\033[38;2;%sm%s", color[(color_index % 12) + 1], substr($0, pos, 1)
                pos++
                color_index++
            } else {
                color_index++
            }

            while (pos <= length($0)) {
                printf "\033[38;2;%sm%s", color[(color_index % 12) + 1], substr($0, pos, 2)
                pos += 2
                color_index++
            }
            printf "\033[0m\n"
        }
    '
}

if [ "$nanokvm_banner_style" = rainbow ]; then
    printf '\n'
    nanokvm_banner_print_rainbow3 <<'NANOKVM_BANNER'
::::    :::     :::     ::::    :::  ::::::::  :::    ::: :::     ::: ::::     ::::       ::::::::   ::::::::
:+:+:   :+:   :+: :+:   :+:+:   :+: :+:    :+: :+:   :+:  :+:     :+: +:+:+: :+:+:+      :+:    :+: :+:    :+:
:+:+:+  +:+  +:+   +:+  :+:+:+  +:+ +:+    +:+ +:+  +:+   +:+     +:+ +:+ +:+:+ +:+      +:+    +:+ +:+
+#+ +:+ +#+ +#++:++#++: +#+ +:+ +#+ +#+    +:+ +#++:++    +#+     +:+ +#+  +:+  +#+      +#+    +:+ +#++:++#++
+#+  +#+#+# +#+     +#+ +#+  +#+#+# +#+    +#+ +#+  +#+    +#+   +#+  +#+       +#+      +#+    +#+        +#+
#+#   #+#+# #+#     #+# #+#   #+#+# #+#    #+# #+#   #+#    #+# #+#   #+#       #+#      #+#    #+# #+#    #+#
###    #### ###     ### ###    ####  ########  ###    ###     ###     ###       ###       ########   ########
NANOKVM_BANNER
    printf '\n'
    nanokvm_banner_print_rainbow3 <<NANOKVM_VERSION
NanoKVM OS $nanokvm_banner_version
NANOKVM_VERSION
    printf '\n'
else
    printf '\n'
    printf '%s  %b%s%b\n' 'NN    N                         KK   KK VV   VV MM   MM' "$nanokvm_banner_accent" ' OOOOO   SSSSS ' "$nanokvm_banner_reset"
    printf '%s  %b%s%b\n' 'NNN   N  aaaaa  nnnnn    ooooo  KK  KK  VV   VV MMM MMM' "$nanokvm_banner_accent" 'OO   OO SS     ' "$nanokvm_banner_reset"
    printf '%s  %b%s%b\n' 'N NN  N      aa nn  nn  oo   oo KK KK   VV   VV MM M MM' "$nanokvm_banner_accent" 'OO   OO  SSSSS ' "$nanokvm_banner_reset"
    printf '%s  %b%s%b\n' 'N  NN N  aaaaaa nn  nn  oo   oo KKKK    VV   VV MM   MM' "$nanokvm_banner_accent" 'OO   OO      SS' "$nanokvm_banner_reset"
    printf '%s  %b%s%b\n' 'N   NNN aa   aa nn  nn  oo   oo KK KK    VV VV  MM   MM' "$nanokvm_banner_accent" 'OO   OO SS   SS' "$nanokvm_banner_reset"
    printf '%s  %b%s%b\n' 'N    NN  aaaaaa nn  nn   ooooo  KK  KK    VVV   MM   MM' "$nanokvm_banner_accent" ' OOOOO   SSSSS ' "$nanokvm_banner_reset"
    printf '\nNanoKVM OS %s\n\n' "$nanokvm_banner_version"
fi

if [ "$(id -u)" -eq 0 ]; then
    nanokvm_banner_symbol='#'
else
    nanokvm_banner_symbol='$'
fi
_nanokvm_prompt_user=$nanokvm_banner_user
_nanokvm_prompt_host=$nanokvm_banner_accent
_nanokvm_prompt_reset=$nanokvm_banner_reset
_nanokvm_prompt_symbol=$nanokvm_banner_symbol
nanokvm_prompt_apply() {
    if [ -n "$_nanokvm_prompt_host" ]; then
        PS1="\[$_nanokvm_prompt_user\]\\u\[$_nanokvm_prompt_reset\]@\[$_nanokvm_prompt_host\]\\h\[$_nanokvm_prompt_reset\]:\\w$_nanokvm_prompt_symbol "
    else
        PS1="\\u@\\h:\\w$_nanokvm_prompt_symbol "
    fi
    export PS1
}
nanokvm_prompt_apply
if [ -n "${BASH_VERSION:-}" ]; then
    case ${PROMPT_COMMAND:-} in
        *nanokvm_prompt_apply*) ;;
        *) PROMPT_COMMAND="${PROMPT_COMMAND:+$PROMPT_COMMAND; }nanokvm_prompt_apply" ;;
    esac
fi

unset -f nanokvm_banner_print_rainbow3
unset nanokvm_banner_version nanokvm_banner_accent nanokvm_banner_user \
    nanokvm_banner_reset nanokvm_banner_symbol nanokvm_banner_style \
    nanokvm_banner_selected_style nanokvm_banner_line
