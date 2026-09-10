#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT="$ROOT/kvmapp/system/init.d/S01fs"
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nanokvm-s01fs-lifecycle.XXXXXX")
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

fail() {
        echo "FAIL: $*" >&2
        exit 1
}

assert_exists() {
        [ -e "$1" ] || fail "missing $1"
}

assert_absent() {
        [ ! -e "$1" ] || fail "unexpected $1"
}

make_stubs() {
        mkdir -p "$TMP_ROOT/bin"

        cat > "$TMP_ROOT/bin/mkfs.exfat" <<'EOF'
#!/bin/sh
printf 'mkfs.exfat %s\n' "$*" >> "$NANOKVM_TEST_LOG"
[ "${NANOKVM_MKFS_FAIL:-0}" != 1 ] || exit 1
: > "$NANOKVM_DATA_PART.hasfs"
EOF

        cat > "$TMP_ROOT/bin/mount" <<'EOF'
#!/bin/sh
printf 'mount %s\n' "$*" >> "$NANOKVM_TEST_LOG"
[ "${NANOKVM_MOUNT_FAIL:-0}" != 1 ] || exit 1
EOF

        chmod +x "$TMP_ROOT/bin/mkfs.exfat" "$TMP_ROOT/bin/mount"
}

reset_case() {
        case_name=$1
        CASE_DIR="$TMP_ROOT/$case_name"
        mkdir -p "$CASE_DIR/data"
        NANOKVM_DATA_PART="$CASE_DIR/mmcblk0p3"
        NANOKVM_DATA_DIR="$CASE_DIR/data"
        NANOKVM_DISK0_MARKER="$CASE_DIR/kvm.disk0"
        NANOKVM_DATA_FORMAT_PENDING="$CASE_DIR/kvm.disk0.formatting"
        NANOKVM_TEST_LOG="$CASE_DIR/log"
        export NANOKVM_DATA_PART NANOKVM_DATA_DIR NANOKVM_DISK0_MARKER
        export NANOKVM_DATA_FORMAT_PENDING NANOKVM_TEST_LOG
        unset NANOKVM_MKFS_FAIL NANOKVM_MOUNT_FAIL
        : > "$NANOKVM_DATA_PART"
        : > "$NANOKVM_TEST_LOG"
}

make_stubs
PATH="$TMP_ROOT/bin:$PATH"
export PATH
set --
# shellcheck disable=SC1090
. "$SCRIPT"

reset_case format_success
touch "$NANOKVM_DATA_FORMAT_PENDING"
format_and_mount_data_partition
assert_exists "$NANOKVM_DATA_PART.hasfs"
assert_exists "$NANOKVM_DISK0_MARKER"
assert_absent "$NANOKVM_DATA_FORMAT_PENDING"
grep -Fx "mkfs.exfat -L data $NANOKVM_DATA_PART" "$NANOKVM_TEST_LOG" >/dev/null \
        || fail 'exFAT label or target is wrong'

reset_case format_failure
touch "$NANOKVM_DATA_FORMAT_PENDING"
NANOKVM_MKFS_FAIL=1
export NANOKVM_MKFS_FAIL
if format_and_mount_data_partition
then
        fail 'mkfs failure was ignored'
fi
assert_exists "$NANOKVM_DATA_FORMAT_PENDING"
assert_absent "$NANOKVM_DISK0_MARKER"

reset_case mount_failure_after_format
touch "$NANOKVM_DATA_FORMAT_PENDING"
NANOKVM_MOUNT_FAIL=1
export NANOKVM_MOUNT_FAIL
if format_and_mount_data_partition
then
        fail 'mount failure was ignored'
fi
assert_exists "$NANOKVM_DATA_PART.hasfs"
assert_absent "$NANOKVM_DATA_FORMAT_PENDING"
assert_absent "$NANOKVM_DISK0_MARKER"

reset_case legacy_empty
dd if=/dev/zero of="$NANOKVM_DATA_PART" bs=1K count=8 2>/dev/null
is_empty_partition "$NANOKVM_DATA_PART" || fail 'zero-filled legacy partition was not recoverable'
printf 'user-data' > "$NANOKVM_DATA_PART"
if is_empty_partition "$NANOKVM_DATA_PART"
then
        fail 'non-empty unknown partition would be formatted'
fi

if grep -q '/dev/mmcblk0p3.*lun\.0/file' "$ROOT/kvmapp/system/init.d/S03usbdev"
then
        fail 'local data partition is still exposed as USB mass storage'
fi

echo 'S01fs data-lifecycle tests passed'
