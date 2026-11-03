#!/bin/sh
# The rainbow SSH banner style (Settings > Security) also colours the
# shell: a rainbow prompt, and everyday text commands shown through lolcat.
# Sourced by Alpine's /etc/profile after nanokvm-banner.sh. Only an
# interactive SSH login on a colour terminal is changed, and a wrapped command
# is coloured only when its output is that terminal: pipes, redirections,
# scripts, scp, sftp and "ssh host command" see the plain output. Full-screen
# programs (vi, top, less, ...) are never wrapped, nor are launchers such
# as env, which would run them through the pipe.
case $- in
    *i*) ;;
    *) return 0 ;;
esac
[ -n "${SSH_CONNECTION:-}" ] && [ -t 1 ] && [ -z "${NO_COLOR:-}" ] || return 0
case ${TERM:-} in ''|dumb) return 0 ;; esac
command -v lolcat >/dev/null 2>&1 || return 0
nanokvm_rainbow_style=
[ -r /etc/kvm/banner-style ] && IFS= read -r nanokvm_rainbow_style < /etc/kvm/banner-style
[ "$nanokvm_rainbow_style" = rainbow ] || { unset nanokvm_rainbow_style; return 0; }
unset nanokvm_rainbow_style

# The banner's colours, one per part of the prompt.
nanokvm_prompt_apply() {
    PS1='\[\033[1;38;2;255;40;40m\]\u\[\033[38;2;255;180;0m\]@\[\033[1;38;2;220;255;0m\]\h'
    PS1="$PS1"'\[\033[38;2;0;255;80m\]:\[\033[1;38;2;0;200;255m\]\w\[\033[38;2;255;0;200m\]'
    if [ "$(id -u)" -eq 0 ]; then PS1="$PS1#"; else PS1="$PS1\$"; fi
    PS1="$PS1"'\[\033[0m\] '
    export PS1
}
nanokvm_prompt_apply

# The command, through lolcat when its output is the terminal. Its own exit
# status is kept (pipefail); errors stay uncoloured on stderr.
nanokvm_rainbow() {
    if [ -t 1 ]; then
        # These forms launch arbitrary programs, which must keep their tty.
        # shellcheck disable=SC3043 # BusyBox ash and bash both have local.
        local arg
        for arg; do
            case "${2-}:$arg" in
                find:-exec|find:-execdir|find:-ok|find:-okdir|ip:exec)
                    "$@"
                    return
                    ;;
            esac
        done
        # shellcheck disable=SC3040 # BusyBox ash and bash have pipefail.
        (set -o pipefail; "$@" | lolcat)
    else
        "$@"
    fi
}
# ls in a pipe would print one name per line and drop its own colours.
ls() { if [ -t 1 ]; then nanokvm_rainbow command ls -C "$@"; else command ls "$@"; fi; }
for nanokvm_rainbow_command in cat df du free ps ip ifconfig uname uptime dmesg date \
        mount lsmod head tail grep find which id hostname; do
    eval "$nanokvm_rainbow_command() { nanokvm_rainbow command $nanokvm_rainbow_command \"\$@\"; }"
done
unset nanokvm_rainbow_command
