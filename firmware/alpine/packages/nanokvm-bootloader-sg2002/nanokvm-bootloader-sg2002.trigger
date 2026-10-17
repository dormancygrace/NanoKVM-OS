#!/bin/sh
set -eu
# Image assembly has no live boot partition or running OpenRC system; the
# image already carries this fip.bin.
[ -f /run/openrc/softlevel ] || exit 0
exec /usr/libexec/nanokvm/activate-bootloader
