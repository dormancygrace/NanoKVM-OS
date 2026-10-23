#!/bin/sh
# check-release-key.sh KEY: KEY (NAME.key, with NAME.pub next to it) is a
# valid EC key whose public half is published in firmware/alpine/keys under
# the same file name, as devices look it up by that name.
set -eu
key=$1
pub=${key%.key}.pub
root=$(cd "$(dirname "$0")/.." && pwd)
"$root/scripts/check-ec-key.sh" "$key" "$pub"
for published in "$root"/firmware/alpine/keys/*.pub; do
    if cmp -s "$pub" "$published"; then
        [ "$(basename "$pub")" = "$(basename "$published")" ] && exit 0
        echo "EC public key filename must match the published filename for APK v2 verification" >&2
        exit 1
    fi
done
echo "$pub is not in firmware/alpine/keys; add it there," >&2
echo "and publish it in nanokvm-keys before signing with it (firmware/alpine/README.md)" >&2
exit 1
