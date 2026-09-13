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

assert_file() {
        [ -e "$1" ] || fail "missing $1"
}

assert_absent() {
        [ ! -e "$1" ] || fail "unexpected $1"
}

assert_contains() {
        grep -F -- "$1" "$2" >/dev/null || fail "$2 does not contain: $1"
}

assert_no_call() {
        if grep -F -- "$1" "$CASE_LOG" >/dev/null; then
                fail "unexpected call '$1' in $CASE_LOG"
        fi
}

BIN_DIR="$TMP_ROOT/bin"
mkdir -p "$BIN_DIR"

cat > "$BIN_DIR/mount" <<'EOF'
#!/bin/sh
printf 'mount %s\n' "$*" >> "$NANOKVM_TEST_LOG"
if [ "${NANOKVM_MOUNT_FAIL:-0}" = 1 ] && [ "${1:-}" = "$NANOKVM_DATA_PART" ]; then
        exit 1
fi
exit 0
EOF

cat > "$BIN_DIR/blkid" <<'EOF'
#!/bin/sh
printf 'blkid %s\n' "$*" >> "$NANOKVM_TEST_LOG"
if [ "$NANOKVM_BLKID_KIND" = recognized ] ||
        [ -e "$NANOKVM_FORMATTED_FILE" ]; then
        printf 'TYPE="exfat"\n'
fi
exit 0
EOF

cat > "$BIN_DIR/mkfs.exfat" <<'EOF'
#!/bin/sh
printf 'mkfs.exfat %s\n' "$*" >> "$NANOKVM_TEST_LOG"
[ "$NANOKVM_MKFS_FAIL" = 1 ] && exit 1
: > "$NANOKVM_FORMATTED_FILE"
exit 0
EOF

cat > "$BIN_DIR/udevadm" <<'EOF'
#!/bin/sh
printf 'udevadm %s\n' "$*" >> "$NANOKVM_TEST_LOG"
exit 0
EOF

cat > "$BIN_DIR/sleep" <<'EOF'
#!/bin/sh
printf 'sleep %s\n' "$*" >> "$NANOKVM_TEST_LOG"
exit 0
EOF

cat > "$BIN_DIR/parted" <<'EOF'
#!/bin/sh
printf 'parted %s\n' "$*" >> "$NANOKVM_TEST_LOG"
count=$(cat "$NANOKVM_PARTED_COUNT")
count=$((count + 1))
printf '%s\n' "$count" > "$NANOKVM_PARTED_COUNT"

if printf '%s\n' "$*" | grep -qw print; then
        root_start=$(cat "$NANOKVM_ROOT_START_FILE")
        root_size=$(cat "$NANOKVM_ROOT_SIZE_FILE")
        p1_end=$((root_start - 1))
        p2_end=$((root_start + root_size - 1))
        data_start=$NANOKVM_TEST_DATA_START
        data_start_post=$data_start
        case "$NANOKVM_TEST_PARTED_MODE" in
                foreign-geometry)
                        p2_start=$((root_start - 1))
                        p2_end=$((p2_start + root_size - 1))
                        ;;
                *)
                        p2_start=$root_start
                        ;;
        esac
        printf 'BYT;\n'
        printf '%s:%ss:file:512:512:msdos::;\n' "$NANOKVM_DISK" "$NANOKVM_TEST_DISK_SECTORS"
        printf '1:1s:%ss:%ss:fat16::boot, lba;\n' "$p1_end" "$p1_end"
        printf '2:%ss:%ss:%ss:ext4::;\n' "$p2_start" "$p2_end" "$root_size"
        if [ "$NANOKVM_TEST_PARTED_MODE" = foreign-p4 ]; then
                printf '4:%ss:%ss:%ss:ext4::;\n' "$((p2_end + 1))" "$((p2_end + 1024))" 1025
        elif [ "$count" -gt 1 ] && [ "$NANOKVM_TEST_PARTED_MODE" = bad-post ]; then
                data_start_post=$((data_start + 2048))
                printf '3:%ss:%ss:%ss:exfat::;\n' "$data_start_post" "$((NANOKVM_TEST_DISK_SECTORS - 1))" "$((NANOKVM_TEST_DISK_SECTORS - data_start_post))"
        elif [ "$count" -gt 1 ] && [ "$NANOKVM_TEST_PARTED_MODE" != late-node ] && [ "$NANOKVM_TEST_PARTED_MODE" != parted-failure ]; then
                printf '3:%ss:%ss:%ss:exfat::;\n' "$data_start" "$((NANOKVM_TEST_DISK_SECTORS - 1))" "$((NANOKVM_TEST_DISK_SECTORS - data_start))"
        fi
        exit 0
fi

if [ "$NANOKVM_TEST_PARTED_MODE" = parted-failure ]; then
        exit 1
fi
if [ "$NANOKVM_TEST_PARTED_MODE" != late-node ]; then
        mv -f "$NANOKVM_DATA_PENDING_NODE" "$NANOKVM_DATA_PART"
fi
exit 0
EOF

chmod +x "$BIN_DIR/mount" "$BIN_DIR/blkid" "$BIN_DIR/mkfs.exfat" "$BIN_DIR/udevadm" "$BIN_DIR/sleep" "$BIN_DIR/parted"
PATH="$BIN_DIR:$PATH"
export PATH

setup_case() {
        case_name=$1
        case_parted_mode=$2
        case_data_kind=$3
        case_marker=$4
        case_pending=$5
        CASE_DIR="$TMP_ROOT/$case_name"
        CASE_LOG="$CASE_DIR/calls.log"
        mkdir -p "$CASE_DIR/boot" "$CASE_DIR/data"
        : > "$CASE_LOG"
        : > "$CASE_DIR/profile"
        : > "$CASE_DIR/start"
        : > "$CASE_DIR/size"
        : > "$CASE_DIR/parted-count"
        printf '32769\n' > "$CASE_DIR/start"
        printf '3145728\n' > "$CASE_DIR/size"
        rm -f "$CASE_DIR/p3" "$CASE_DIR/p3-pending" "$CASE_DIR/ready"                 "$CASE_DIR/pending" "$CASE_DIR/formatted" "$CASE_DIR/boot/usb.disk0"
        export CASE_LOG
        export NANOKVM_TEST_LOG="$CASE_LOG"
        export NANOKVM_DISK=/dev/loop0
        export NANOKVM_BOOT_PART=/dev/loop2
        export NANOKVM_ROOT_PART=/dev/loop3
        export NANOKVM_DATA_PART="$CASE_DIR/p3"
        export NANOKVM_BOOT_DIR="$CASE_DIR/boot"
        export NANOKVM_DATA_DIR="$CASE_DIR/data"
        export NANOKVM_DISK0_MARKER="$CASE_DIR/ready"
        export NANOKVM_DATA_FORMAT_PENDING="$CASE_DIR/pending"
        export NANOKVM_PROFILE="$CASE_DIR/profile"
        export NANOKVM_ROOT_START_FILE="$CASE_DIR/start"
        export NANOKVM_ROOT_SIZE_FILE="$CASE_DIR/size"
        export NANOKVM_DATA_START_FILE="$CASE_DIR/data-start"
        export NANOKVM_DATA_SIZE_FILE="$CASE_DIR/data-size"
        export NANOKVM_PARTED_COUNT="$CASE_DIR/parted-count"
        export NANOKVM_DATA_PENDING_NODE="$CASE_DIR/p3-pending"
        export NANOKVM_FORMATTED_FILE="$CASE_DIR/formatted"
        export NANOKVM_TEST_PARTED_MODE="$case_parted_mode"
        export NANOKVM_TEST_DISK_SECTORS=3350000
        export NANOKVM_TEST_DATA_START=3180544
        export NANOKVM_DATA_ALIGNMENT_SECTORS=2048
        export NANOKVM_DATA_MIN_SECTORS=32768
        export NANOKVM_DATA_NODE_ATTEMPTS=3
        export NANOKVM_DATA_NODE_DELAY=0
        export NANOKVM_PARTED="$BIN_DIR/parted"
        export NANOKVM_MKFS_EXFAT="$BIN_DIR/mkfs.exfat"
        export NANOKVM_BLKID="$BIN_DIR/blkid"
        export NANOKVM_UDEVADM="$BIN_DIR/udevadm"
        export NANOKVM_SLEEP="$BIN_DIR/sleep"
        export NANOKVM_BLKID_KIND=unknown
        export NANOKVM_MKFS_FAIL=0
        export NANOKVM_MOUNT_FAIL=0
        : > "$CASE_DIR/data-start"
        : > "$CASE_DIR/data-size"
        printf '%s\n' "$NANOKVM_TEST_DATA_START" > "$CASE_DIR/data-start"
        printf '%s\n' "$((NANOKVM_TEST_DISK_SECTORS - NANOKVM_TEST_DATA_START))" > "$CASE_DIR/data-size"
        if [ "$case_marker" = on ]; then
                : > "$CASE_DIR/boot/usb.disk0"
        fi
        if [ "$case_pending" = on ]; then
                printf 'old-pending\n' > "$NANOKVM_DATA_FORMAT_PENDING"
        fi
        case "$case_data_kind" in
                recognized|unknown)
                        ln -s /dev/loop1 "$NANOKVM_DATA_PART"
                        [ "$case_data_kind" = recognized ] && NANOKVM_BLKID_KIND=recognized
                        export NANOKVM_BLKID_KIND
                        ;;
                regular)
                        : > "$NANOKVM_DATA_PART"
                        ;;
                none)
                        ;;
                *)
                        fail "unknown data kind: $case_data_kind"
                        ;;
        esac
        if [ "$case_data_kind" = none ]; then
                ln -s /dev/loop1 "$NANOKVM_DATA_PENDING_NODE"
        fi
}

run_script() {
        CASE_STDOUT="$CASE_DIR/stdout"
        CASE_STDERR="$CASE_DIR/stderr"
        set +e
        "$SCRIPT" start > "$CASE_STDOUT" 2> "$CASE_STDERR"
        CASE_STATUS=$?
        set -e
}

expect_status() {
        [ "$CASE_STATUS" -eq "$1" ] || {
                cat "$CASE_STDOUT" >&2
                cat "$CASE_STDERR" >&2
                fail "$CASE_DIR returned $CASE_STATUS, expected $1"
        }
}

# A larger card with the opt-in marker creates p3, verifies the table/node
# identity, formats exFAT, and mounts it. The p3 symlink is only made visible by
# the synthetic parted command after the preflight proves it was absent.
setup_case fresh-success normal none on off
run_script
expect_status 0
assert_absent "$NANOKVM_DATA_FORMAT_PENDING"
assert_file "$NANOKVM_DISK0_MARKER"
assert_contains 'mkfs.exfat -L data' "$CASE_LOG"
assert_contains 'mkpart primary ntfs 3180544s 100%' "$CASE_LOG"

# A second boot sees the same resolved block node and mounts it without
# touching the partition table or invoking the formatter.
: > "$CASE_LOG"
run_script
expect_status 0
assert_absent "$NANOKVM_DATA_FORMAT_PENDING"
assert_file "$NANOKVM_DISK0_MARKER"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'
assert_contains 'mount ' "$CASE_LOG"

# The exact portable image has no sectors after p2. It fails clearly before
# touching the partition table or invoking mkfs.
setup_case exact-no-space normal none on off
export NANOKVM_TEST_DISK_SECTORS=3178497
run_script
expect_status 1
assert_contains 'No space remains after the root partition' "$CASE_STDERR"
assert_no_call 'mkpart'
assert_no_call 'mkfs.exfat'

# A card with less than the documented 16 MiB minimum data volume is rejected.
setup_case undersized-card normal none on off
export NANOKVM_TEST_DISK_SECTORS=3210000
run_script
expect_status 1
assert_contains 'at least 32768 sectors' "$CASE_STDERR"
assert_no_call 'mkpart'
assert_no_call 'mkfs.exfat'

# Existing extra/foreign partitions and mismatched root geometry are preserved.
setup_case foreign-p4 foreign-p4 none on off
run_script
expect_status 1
assert_contains 'Unsupported DOS layout' "$CASE_STDERR"
assert_no_call 'mkpart'
assert_no_call 'mkfs.exfat'

setup_case foreign-geometry foreign-geometry none on off
run_script
expect_status 1
assert_contains 'Unsupported DOS layout' "$CASE_STDERR"
assert_no_call 'mkpart'
assert_no_call 'mkfs.exfat'

# A failed parted call leaves only a diagnostic pending marker; no formatter
# runs, and a later boot cannot infer ownership from that marker.
setup_case parted-failure parted-failure none on off
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_contains 'retry marker retained' "$CASE_STDERR"
assert_no_call 'mkfs.exfat'

# Missing or mismatched node/table readback is bounded and never formatted.
setup_case late-node late-node none on off
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_contains 'node/layout did not appear' "$CASE_STDERR"
assert_contains 'udevadm settle --timeout=1' "$CASE_LOG"
assert_no_call 'mkfs.exfat'

setup_case bad-post bad-post none on off
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_contains 'node/layout did not appear' "$CASE_STDERR"
assert_no_call 'mkfs.exfat'

# Formatter and mount failures retain the diagnostic marker and never claim
# that the new filesystem is ready.
setup_case format-failure normal none on off
export NANOKVM_MKFS_FAIL=1
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_file "$NANOKVM_DATA_PART"
assert_absent "$NANOKVM_DISK0_MARKER"
assert_contains 'Could not format newly-created data partition' "$CASE_STDERR"

setup_case mount-failure-after-format normal none on off
export NANOKVM_MOUNT_FAIL=1
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_file "$NANOKVM_DATA_PART"
assert_file "$NANOKVM_FORMATTED_FILE"
assert_absent "$NANOKVM_DISK0_MARKER"
assert_contains 'formatted but could not be mounted' "$CASE_STDERR"

# A recognized existing filesystem may mount and clears only a stale pending
# marker; it never invokes parted or mkfs.
setup_case existing-recognized normal recognized on on
run_script
expect_status 0
assert_absent "$NANOKVM_DATA_FORMAT_PENDING"
assert_file "$NANOKVM_DISK0_MARKER"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

# Unknown existing data is preserved and produces an actionable failure,
# regardless of the ready/pending markers.
setup_case existing-unknown normal unknown on on
run_script
expect_status 1
assert_file "$NANOKVM_DATA_PART"
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_absent "$NANOKVM_DISK0_MARKER"
assert_contains 'Unknown data partition preserved' "$CASE_STDERR"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

# Pending without a live p3 is never treated as permission to create or format.
setup_case pending-without-node normal none on on
run_script
expect_status 1
assert_file "$NANOKVM_DATA_FORMAT_PENDING"
assert_contains 'no verified p3' "$CASE_STDERR"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

# A regular file or dangling symlink at the data path cannot be mistaken for
# a block device and does not reach partitioning.
setup_case regular-data-path normal regular on off
run_script
expect_status 1
assert_contains 'not a block device' "$CASE_STDERR"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

# A dangling symlink is rejected even though a valid symlink to a block node
# is accepted as an existing device path.
setup_case dangling-symlink normal none on off
ln -s "$CASE_DIR/missing-device" "$NANOKVM_DATA_PART"
run_script
expect_status 1
assert_contains 'not a block device' "$CASE_STDERR"
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

# Without the opt-in marker, a fresh p1/p2 image remains unchanged.
setup_case marker-absent normal none off off
run_script
expect_status 0
assert_no_call 'parted '
assert_no_call 'mkfs.exfat'

echo 'S01fs data-lifecycle tests passed'
