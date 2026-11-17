#!/bin/sh
# profile.sh PATHS OUTDIR [NAME SECONDS] (on the device, debug kernel): ftrace function profiles (hit counts) for
# generate.py. Without NAME: the system-call paths through PATHS (paths.c, a static build) and the baseline
# "nop" (its start and exit). With NAME SECONDS: the whole system for SECONDS, while network traffic for that
# path runs from a peer (ethtx, ethrx, wifitx, wifirx: 12 MiB in one direction) or nothing runs (idle).
set -e
P=$1; O=$2
T=/sys/kernel/tracing
[ -e $T/function_profile_enabled ] || mount -t tracefs nodev $T
mkdir -p "$O"
d=
cleanup() {
	echo 0 > $T/function_profile_enabled
	[ -z "$d" ] || rm -rf "$d"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
run() { # NAME CMD...
	n=$1; shift
	echo 0 > $T/function_profile_enabled
	echo 1 > $T/function_profile_enabled
	"$@" > /dev/null
	echo 0 > $T/function_profile_enabled
	cat $T/trace_stat/function0 > "$O/fprof-$n.txt"
}
if [ -n "$3" ]; then
	run "$3" sleep "$4"
	exit
fi
d=$(mktemp -d)
run nop "$P" getppid 0 "$d"
for op in pipe stat open getppid read write; do run $op "$P" $op 2000 "$d"; done
run fault "$P" fault 12800 "$d"
