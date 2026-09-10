#!/bin/sh

set -eu

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
hid_profile="$root_dir/kvmapp/system/init.d/S03usbhid"
full_profile="$root_dir/kvmapp/system/init.d/S03usbdev"

fail() {
    echo "USB profile test failed: $*" >&2
    exit 1
}

count_links() {
    pattern=$1
    file=$2
    count=$(grep -Ec "$pattern" "$file" || true)
    printf '%s' "$count"
}

[ "$(count_links 'ln -s functions/hid\.GS[0-9]' "$hid_profile")" -eq 3 ] ||
    fail "HID-only profile must retain keyboard, relative mouse and absolute pointer"
[ "$(count_links 'ln -s functions/acm\.GS0' "$hid_profile")" -eq 1 ] ||
    fail "HID-only profile must expose exactly one optional ACM function"
[ "$(count_links 'ln -s functions/acm\.GS0' "$full_profile")" -eq 1 ] ||
    fail "full profile must expose exactly one optional ACM function"

if grep -Eq 'functions/(ncm|rndis|mass_storage)\.' "$hid_profile"; then
    fail "HID + ACM profile must not allocate USB network or mass-storage endpoints"
fi

grep -q '/boot/usb.acm' "$hid_profile" || fail "ACM function must be opt-in"
grep -q '0x0624 > bcdDevice' "$hid_profile" ||
    fail "ACM profile must have a distinct USB descriptor revision"

# Endpoint accounting from the Linux gadget functions used here:
# HID = 1 IN + 1 OUT each, ACM = 2 IN + 1 OUT. The live SG2002 has seven
# device endpoint numbers and six configured non-zero dedicated TX FIFOs.
hid_in=3
hid_out=3
acm_in=2
acm_out=1
[ $((hid_in + acm_in)) -le 6 ] || fail "HID + ACM exceeds configured IN FIFOs"
[ $((hid_out + acm_out)) -le 7 ] || fail "HID + ACM exceeds OUT endpoint numbers"

# The full profile has six IN endpoints before ACM: three HID, two NCM/RNDIS
# and one mass-storage. The server-side composition validator must therefore
# prevent enabling ACM until enough optional functions have been disabled.
grep -q '/boot/usb.acm' "$full_profile" || fail "full profile ACM must be opt-in"

echo "USB profile checks passed"
