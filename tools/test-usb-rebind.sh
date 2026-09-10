#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for profile in S03usbdev S03usbhid
do
    (
        # Sourcing with an unrecognized command only defines the functions.
        set -- test
        . "$root/kvmapp/system/init.d/$profile"
        gadget="$stage/$profile"
        mkdir -p "$gadget/configs/c.1/strings" "$gadget/functions/hid.GS0" "$gadget/os_desc"
        printf 'controller\n' > "$gadget/UDC"
        printf '120\n' > "$gadget/configs/c.1/MaxPower"
        ln -s ../../functions/hid.GS0 "$gadget/configs/c.1/hid.GS0"
        ln -s ../../functions/rndis.usb0 "$gadget/configs/c.1/old-network"
        ln -s ../configs/c.1 "$gadget/os_desc/c.1"
        reset_usb_config_links "$gadget"
        test -z "$(cat "$gadget/UDC")"
        test ! -L "$gadget/configs/c.1/hid.GS0"
        test ! -L "$gadget/configs/c.1/old-network"
        test ! -L "$gadget/os_desc/c.1"
        test -d "$gadget/functions/hid.GS0"
        test -d "$gadget/configs/c.1/strings"
        test "$(cat "$gadget/configs/c.1/MaxPower")" = 120
        reset_usb_config_links "$gadget"
        reset_usb_config_links "$gadget-not-created"
    )
done
echo 'USB rebind cleanup tests passed'
